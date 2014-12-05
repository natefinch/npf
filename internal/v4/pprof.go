package v4

import (
	"net/http"
	"net/http/pprof"
	runtimepprof "runtime/pprof"
	"strings"
	"text/template"

	"github.com/juju/charmstore/internal/router"
)

type pprofHandler struct {
	mux  *http.ServeMux
	auth authenticator
}

type authenticator interface {
	authenticate(w http.ResponseWriter, req *http.Request) error
}

func newPprofHandler(auth authenticator) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/cmdline", pprof.Cmdline)
	mux.HandleFunc("/profile", pprof.Profile)
	mux.HandleFunc("/symbol", pprof.Symbol)
	mux.HandleFunc("/", pprofIndex)
	return &pprofHandler{
		mux:  mux,
		auth: auth,
	}
}

func (h *pprofHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if err := h.auth.authenticate(w, req); err != nil {
		router.WriteError(w, err)
		return
	}
	h.mux.ServeHTTP(w, req)
}

// pprofIndex is copied from pprof.Index with minor modifications
// to make it work using a relative path.
func pprofIndex(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/" {
		profiles := runtimepprof.Profiles()
		if err := indexTmpl.Execute(w, profiles); err != nil {
			logger.Errorf("cannot execute pprof template: %v", err)
		}
		return
	}
	name := strings.TrimPrefix(req.URL.Path, "/")
	pprof.Handler(name).ServeHTTP(w, req)
}

var indexTmpl = template.Must(template.New("index").Parse(`<html>
<head>
<title>pprof</title>
</head>
pprof<br>
<br>
<body>
profiles:<br>
<table>
{{range .}}
<tr><td align=right>{{.Count}}<td><a href="{{.Name}}?debug=1">{{.Name}}</a>
{{end}}
</table>
<br>
<a href="goroutine?debug=2">full goroutine stack dump</a><br>
</body>
</html>
`))
