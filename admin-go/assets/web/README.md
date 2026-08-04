# Embedded Angular Build

`root/` is a generated production build copied from `admin/webapp/root`. The Go compiler embeds
the directory into the Manager binary; `root/index.html` is intentionally named explicitly in the
embed pattern so builds fail when the UI has not been generated.

To refresh the snapshot, run the production Angular build in `admin/webapp`, then replace the
contents of this `root/` directory with the resulting `admin/webapp/root/` contents. RW-006 will
automate this step in the production image build.
