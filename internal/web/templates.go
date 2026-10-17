package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"finance.home/internal/money"
)

//go:embed templates/*.html static/css/* static/js/* static/vendor/*
var FS embed.FS

var Funcs = template.FuncMap{
	"money":      money.Format,
	"moneyAbs":   money.FormatAbs,
	"centsInput": func(cents int64) string {
		neg := cents < 0
		if neg {
			cents = -cents
		}
		s := fmt.Sprintf("%d.%02d", cents/100, cents%100)
		if neg {
			return "-" + s
		}
		return s
	},
	"today":      func() string { return time.Now().Format("2006-01-02") },
	"yesterday":  func() string { return time.Now().AddDate(0, 0, -1).Format("2006-01-02") },
	"eq":         func(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) },
	"ne":         func(a, b any) bool { return fmt.Sprint(a) != fmt.Sprint(b) },
	"hasPrefix":  strings.HasPrefix,
	"lower":      strings.ToLower,
	"dict":       dict,
	"add":        func(a, b int) int { return a + b },
	"ptrID": func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	},
	"sub": func(a, b int) int { return a - b },
	"containsInt": func(ids []int64, id int64) bool {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
		return false
	},
	"containsStr": func(vals []string, s string) bool {
		for _, x := range vals {
			if x == s {
				return true
			}
		}
		return false
	},
	// Full href attribute — html/template URL-escapes bare query strings in href="…".
	"txFilterAttr": func(query string) template.HTMLAttr {
		if query == "" {
			return template.HTMLAttr(`href="/transactions"`)
		}
		return template.HTMLAttr(`href="/transactions?` + query + `"`)
	},
	"personLabel": func(slot, nameA, nameB string) string {
		switch slot {
		case "a":
			return nameA
		case "b":
			return nameB
		default:
			return "Shared"
		}
	},
	"accountTypeLabel": func(t string) string {
		switch t {
		case "checking":
			return "Checking"
		case "savings":
			return "Savings"
		case "cash":
			return "Cash"
		case "credit":
			return "Credit card"
		case "loan":
			return "Loan"
		default:
			return t
		}
	},
	"ownerLabel": func(o, nameA, nameB string) string {
		switch o {
		case "a":
			return nameA
		case "b":
			return nameB
		case "joint":
			return "Joint"
		default:
			return o
		}
	},
	"asJS": func(s string) template.JS {
		if s == "" {
			return "null"
		}
		return template.JS(s)
	},
}

func dict(values ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(values); i += 2 {
		m[fmt.Sprint(values[i])] = values[i+1]
	}
	return m
}

type Renderer struct {
	t *template.Template
}

func NewRenderer() (*Renderer, error) {
	t, err := template.New("").Funcs(Funcs).ParseFS(FS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Renderer{t: t}, nil
}

func (r *Renderer) Render(w io.Writer, name string, data any) error {
	return r.t.ExecuteTemplate(w, name, data)
}

func (r *Renderer) RenderHTTP(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := r.Render(&buf, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (r *Renderer) RenderString(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := r.Render(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func StaticHandler() http.Handler {
	sub, err := fs.Sub(FS, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}
