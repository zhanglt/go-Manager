package server

import (
	"bytes"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/hashutil"
)

type staticHandler struct {
	files       fs.FS
	pathPrefix  string
	development bool
	versionHash string
}

func newStaticHandler(files fs.FS, pathPrefix string, development bool, version string) *staticHandler {
	return &staticHandler{
		files: files, pathPrefix: pathPrefix, development: development,
		versionHash: hashutil.CompatibilityHash(version)[:10],
	}
}

func (h *staticHandler) handle(c *gin.Context) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		h.notFound(c)
		return
	}

	resource, root, ok := h.resourcePath(c.Request.URL.Path)
	if !ok {
		h.notFound(c)
		return
	}
	if root {
		h.redirectToCurrentIndex(c)
		return
	}
	if resource == "index.html" {
		if c.Query("v") != h.versionHash {
			h.redirectToCurrentIndex(c)
			return
		}
		h.serve(c, "index.html", "index.html", true)
		return
	}
	if resource == "favicon.ico" {
		c.Writer.Header().Del("Cache-Control")
		h.notFound(c)
		return
	}

	logicalName := resource
	sourceName := resource
	if strings.EqualFold(path.Ext(resource), ".js") {
		c.Header("Content-Type", "application/javascript")
		if !h.development {
			sourceName += ".gz"
			c.Header("Content-Encoding", "gzip")
		}
	}
	if h.serve(c, logicalName, sourceName, false) {
		return
	}
	if h.isPageNavigation(c, resource) {
		h.serve(c, "index.html", "index.html", true)
		return
	}
	if sourceName != logicalName {
		c.Writer.Header().Del("Content-Encoding")
		c.Writer.Header().Del("Content-Type")
	}
	h.notFound(c)
}

func (h *staticHandler) resourcePath(requestPath string) (resource string, root bool, ok bool) {
	if h.pathPrefix != "" {
		if requestPath == h.pathPrefix || requestPath == h.pathPrefix+"/" {
			return "", true, true
		}
		if !strings.HasPrefix(requestPath, h.pathPrefix+"/") {
			return "", false, false
		}
		requestPath = strings.TrimPrefix(requestPath, h.pathPrefix+"/")
	} else {
		if requestPath == "/" {
			return "", true, true
		}
		requestPath = strings.TrimPrefix(requestPath, "/")
	}

	resource = strings.TrimSuffix(requestPath, "/")
	if resource == "" || strings.Contains(resource, "\\") || !fs.ValidPath(resource) {
		return "", false, false
	}
	return resource, false, true
}

func (h *staticHandler) redirectToCurrentIndex(c *gin.Context) {
	target := &url.URL{Path: h.pathPrefix + "/index.html"}
	query := target.Query()
	query.Set("v", h.versionHash)
	target.RawQuery = query.Encode()
	http.Redirect(c.Writer, c.Request, target.String(), http.StatusMovedPermanently)
	c.Abort()
}

func (h *staticHandler) serve(c *gin.Context, logicalName, sourceName string, html bool) bool {
	file, err := h.files.Open(sourceName)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}

	var content io.ReadSeeker
	if seeker, ok := file.(io.ReadSeeker); ok {
		content = seeker
	} else {
		data, readErr := io.ReadAll(file)
		if readErr != nil {
			return false
		}
		content = bytes.NewReader(data)
	}
	if html {
		c.Header("Content-Type", "text/html; charset=utf-8")
	} else {
		c.Writer.Header().Del("Cache-Control")
		if c.Writer.Header().Get("Content-Type") == "" {
			if contentType := mime.TypeByExtension(path.Ext(logicalName)); contentType != "" {
				c.Header("Content-Type", contentType)
			}
		}
	}
	// ServeContent deliberately omits this when Content-Encoding is set; the Scala response includes it.
	c.Header("Content-Length", strconv.FormatInt(info.Size(), 10))
	http.ServeContent(c.Writer, c.Request, logicalName, info.ModTime(), content)
	c.Abort()
	return true
}

func (h *staticHandler) isPageNavigation(c *gin.Context, resource string) bool {
	return path.Ext(path.Base(resource)) == "" &&
		strings.Contains(strings.ToLower(c.GetHeader("Accept")), "text/html")
}

func (h *staticHandler) notFound(c *gin.Context) {
	c.Data(http.StatusNotFound, "text/plain; charset=utf-8", []byte("404 page not found"))
	c.Abort()
}
