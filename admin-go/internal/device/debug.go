package device

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
)

const defaultDebugFileTTL = 10 * time.Minute
const accessExecute = 1

type supportJob struct {
	path     string
	cancel   context.CancelFunc
	done     chan struct{}
	finished bool
	err      error
	timer    *time.Timer
}

func (h *Handler) CreateDebugLog(c *gin.Context) {
	if !h.validateDebugToken(c) {
		return
	}
	enforcer, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeBodyReadError(c, err)
		return
	}
	value := string(enforcer)
	if value != "" && !debugEnforcerID.MatchString(value) {
		writeDebugText(c, http.StatusBadRequest, "invalid enforcer id")
		return
	}
	token := c.GetHeader("Token")
	cluster, _ := h.sessions.Cluster(token)
	key := h.debugKey(token)
	if previous, found := h.detachJob(key); found {
		h.cancelJob(previous)
		<-previous.done
	}
	select {
	case h.supportSlots <- struct{}{}:
	default:
		writeDebugText(c, http.StatusTooManyRequests, "Too many support collections are running.")
		return
	}
	releaseSlot := true
	defer func() {
		if releaseSlot {
			<-h.supportSlots
		}
	}()

	path, err := h.reserveDebugPath()
	if err != nil {
		writeDebugText(c, http.StatusInternalServerError, "Internal server error")
		return
	}
	target, err := h.resolver.Resolve(c.GetHeader("Token"), controller.V1, "auth")
	if err != nil {
		_ = os.Remove(path)
		writeDebugText(c, http.StatusInternalServerError, "Internal server error")
		return
	}
	args := []string{"-s", target.Hostname(), "-j", cluster, "-o", path}
	if value != "" {
		args = append(args, "-e", value)
	}

	jobContext, cancel := context.WithTimeout(h.lifecycle, h.supportTimeout)
	command := h.supportCmd(jobContext, h.supportCommand, args...)
	command.Env = supportEnvironment(command.Environ(), token, h.debugSession(token))
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	job := &supportJob{path: path, cancel: cancel, done: make(chan struct{})}
	old, replaced, accepted := h.replaceJob(key, job)
	if !accepted {
		cancel()
		_ = os.Remove(path)
		writeDebugText(c, http.StatusServiceUnavailable, "Server is shutting down.")
		return
	}
	releaseSlot = false
	if replaced {
		h.cancelJob(old)
	}
	if err := command.Start(); err != nil {
		h.finishJob(key, job, fmt.Errorf("start support command: %w", err))
		writeDebugText(c, http.StatusInternalServerError, "Internal server error")
		return
	}
	go func() {
		err := command.Wait()
		if err == nil {
			err = h.validateDebugFile(path)
		}
		h.finishJob(key, job, err)
	}()
	c.Data(http.StatusAccepted, "text/plain; charset=UTF-8", []byte("Started to collect debug log."))
}

func (h *Handler) CheckDebugLog(c *gin.Context) {
	if !h.validateDebugToken(c) {
		return
	}
	job, found := h.debugJob(c.GetHeader("Token"))
	if !found {
		writeDebugText(c, http.StatusForbidden, "File can not be accessed.")
		return
	}
	h.jobsMu.Lock()
	finished, err := job.finished, job.err
	h.jobsMu.Unlock()
	if !finished {
		writeDebugText(c, http.StatusPartialContent, "In progress")
		return
	}
	if err != nil {
		writeDebugText(c, http.StatusInternalServerError, "Support collection failed.")
		return
	}
	writeDebugText(c, http.StatusOK, "Ready")
}

func (h *Handler) GetDebugLog(c *gin.Context) {
	if !h.validateDebugToken(c) {
		return
	}
	key := h.debugKey(c.GetHeader("Token"))
	job, found := h.takeReadyJob(key)
	if !found {
		writeDebugText(c, http.StatusForbidden, "File can not be accessed.")
		return
	}
	defer os.Remove(job.path)
	opened, info, err := h.openDebugFile(job.path)
	if err != nil {
		writeDebugText(c, http.StatusInternalServerError, "Internal server error")
		return
	}
	defer opened.Close()
	c.Header("Content-Disposition", `inline; filename="debug.gz"`)
	c.DataFromReader(http.StatusOK, info.Size(), "application/x-gzip", opened, nil)
}

func (h *Handler) reserveDebugPath() (string, error) {
	file, err := os.CreateTemp(h.tempDir, "debug-*.gz")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Chmod(0600); err != nil {
		file.Close()
		os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func (h *Handler) validateDebugFile(path string) error {
	file, _, err := h.openDebugFile(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("invalid gzip output: %w", err)
	}
	written, copyErr := io.Copy(io.Discard, io.LimitReader(reader, h.maxDebugFileBytes+1))
	closeErr := reader.Close()
	if copyErr != nil {
		return fmt.Errorf("read gzip output: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close gzip output: %w", closeErr)
	}
	if written > h.maxDebugFileBytes {
		return errors.New("uncompressed support output exceeds the configured limit")
	}
	return nil
}

func (h *Handler) openDebugFile(path string) (*os.File, os.FileInfo, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > h.maxDebugFileBytes {
		file.Close()
		return nil, nil, errors.New("support output is not a secure regular file")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != os.Geteuid() {
		file.Close()
		return nil, nil, errors.New("support output has an unexpected owner")
	}
	return file, info, nil
}

func validateSupportEnvironment(options SupportOptions) error {
	if options.Timeout <= 0 || options.MaxFileBytes <= 0 || options.MaxConcurrent < 1 {
		return errors.New("support limits must be positive")
	}
	if !filepath.IsAbs(options.Command) || !filepath.IsAbs(options.TempDir) {
		return errors.New("support command and temporary directory must be absolute paths")
	}
	commandInfo, err := os.Lstat(options.Command)
	if err != nil {
		return fmt.Errorf("validate support command: %w", err)
	}
	if !commandInfo.Mode().IsRegular() || commandInfo.Mode().Perm()&0111 == 0 || commandInfo.Mode().Perm()&0022 != 0 {
		return errors.New("support command must be a regular executable that is not group/world writable")
	}
	if err := syscall.Access(options.Command, accessExecute); err != nil {
		return fmt.Errorf("support command is not executable by the manager user: %w", err)
	}
	if err := os.MkdirAll(options.TempDir, 0700); err != nil {
		return fmt.Errorf("create support temporary directory: %w", err)
	}
	dirInfo, err := os.Lstat(options.TempDir)
	if err != nil {
		return fmt.Errorf("validate support temporary directory: %w", err)
	}
	stat, ownerOK := dirInfo.Sys().(*syscall.Stat_t)
	if !dirInfo.IsDir() || dirInfo.Mode().Perm()&0077 != 0 || !ownerOK || int(stat.Uid) != os.Geteuid() {
		return errors.New("support temporary directory must be an owner-only directory owned by the manager user")
	}
	probe, err := os.CreateTemp(options.TempDir, ".manager-write-test-*")
	if err != nil {
		return fmt.Errorf("support temporary directory is not writable: %w", err)
	}
	probePath := probe.Name()
	if closeErr := probe.Close(); closeErr != nil {
		_ = os.Remove(probePath)
		return fmt.Errorf("close support temporary directory probe: %w", closeErr)
	}
	if err := os.Remove(probePath); err != nil {
		return fmt.Errorf("remove support temporary directory probe: %w", err)
	}
	return nil
}

func supportEnvironment(environment []string, token, session string) []string {
	filtered := make([]string, 0, len(environment)+2)
	for _, entry := range environment {
		if strings.HasPrefix(entry, "NV_SUPPORT_TOKEN=") || strings.HasPrefix(entry, "NV_SUPPORT_RESS=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, "NV_SUPPORT_TOKEN="+token, "NV_SUPPORT_RESS="+session)
}

func (h *Handler) replaceJob(key string, job *supportJob) (*supportJob, bool, bool) {
	h.jobsMu.Lock()
	defer h.jobsMu.Unlock()
	if h.closed {
		return nil, false, false
	}
	old, found := h.debugJobs[key]
	delete(h.debugJobs, key)
	h.debugJobs[key] = job
	h.jobsWG.Add(1)
	return old, found, true
}

func (h *Handler) detachJob(key string) (*supportJob, bool) {
	h.jobsMu.Lock()
	defer h.jobsMu.Unlock()
	job, found := h.debugJobs[key]
	if found {
		delete(h.debugJobs, key)
		if job.timer != nil {
			job.timer.Stop()
		}
	}
	return job, found
}

func (h *Handler) finishJob(key string, job *supportJob, err error) {
	if err != nil {
		_ = os.Remove(job.path)
	}
	h.jobsMu.Lock()
	job.finished = true
	job.err = err
	job.cancel()
	job.timer = time.AfterFunc(h.debugFileTTL, func() { h.expireJob(key, job) })
	close(job.done)
	h.jobsMu.Unlock()
	<-h.supportSlots
	h.jobsWG.Done()
}

func (h *Handler) expireJob(key string, job *supportJob) {
	h.jobsMu.Lock()
	if h.debugJobs[key] == job {
		delete(h.debugJobs, key)
	}
	h.jobsMu.Unlock()
	_ = os.Remove(job.path)
}

func (h *Handler) cancelJob(job *supportJob) {
	job.cancel()
	_ = os.Remove(job.path)
}

func (h *Handler) takeReadyJob(key string) (*supportJob, bool) {
	h.jobsMu.Lock()
	defer h.jobsMu.Unlock()
	job, found := h.debugJobs[key]
	if !found || !job.finished || job.err != nil {
		return nil, false
	}
	delete(h.debugJobs, key)
	if job.timer != nil {
		job.timer.Stop()
	}
	return job, true
}

func (h *Handler) Close() error {
	h.jobsMu.Lock()
	if h.closed {
		h.jobsMu.Unlock()
		return nil
	}
	h.closed = true
	h.cancelLifecycle()
	jobs := make([]*supportJob, 0, len(h.debugJobs))
	for _, job := range h.debugJobs {
		jobs = append(jobs, job)
		job.cancel()
		if job.timer != nil {
			job.timer.Stop()
		}
	}
	h.debugJobs = make(map[string]*supportJob)
	h.jobsMu.Unlock()
	for _, job := range jobs {
		<-job.done
		_ = os.Remove(job.path)
	}
	h.jobsWG.Wait()
	return nil
}

func (h *Handler) validateDebugToken(c *gin.Context) bool {
	target, err := h.resolver.Resolve(c.GetHeader("Token"), controller.V1, "auth")
	if err != nil {
		writeDebugText(c, http.StatusInternalServerError, "Internal server error")
		return false
	}
	response, err := h.controller.DoTarget(c.Request.Context(), http.MethodPatch, target, strings.NewReader(""), h.debugHeaders(c.GetHeader("Token")))
	if err != nil {
		writeDebugText(c, http.StatusInternalServerError, "Internal server error")
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	switch response.StatusCode {
	case http.StatusOK:
		return true
	case http.StatusUnauthorized:
		writeDebugText(c, http.StatusUnauthorized, "Authentication failed!")
	case http.StatusRequestTimeout:
		writeDebugText(c, http.StatusRequestTimeout, "Session expired!")
	case http.StatusServiceUnavailable:
		writeDebugText(c, http.StatusServiceUnavailable, "Server is not available!")
	default:
		writeDebugText(c, http.StatusInternalServerError, "Internal server error")
	}
	return false
}

func writeDebugText(c *gin.Context, status int, body string) {
	c.Data(status, "text/plain; charset=UTF-8", []byte(body))
}

func (h *Handler) debugHeaders(token string) http.Header {
	headers := make(http.Header)
	headers.Set("X-Auth-Token", token)
	headers.Set("Accept-Encoding", "gzip")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("X-R-Sess", h.debugSession(token))
	return headers
}

func (h *Handler) debugSession(token string) string {
	entry, _ := h.sessions.Get(token)
	return entry.SUSEToken
}

func (h *Handler) debugKey(token string) string {
	cluster, _ := h.sessions.Cluster(token)
	return token + "\x00" + cluster
}

func (h *Handler) debugJob(token string) (*supportJob, bool) {
	h.jobsMu.Lock()
	defer h.jobsMu.Unlock()
	job, found := h.debugJobs[h.debugKey(token)]
	return job, found
}

func (h *Handler) debugFile(token string) (debugFile, bool) {
	job, found := h.debugJob(token)
	if !found || filepath.Dir(job.path) != h.tempDir {
		return debugFile{}, false
	}
	return debugFile{Path: job.path}, true
}
