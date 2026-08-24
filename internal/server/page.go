package server

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// pageData is the view model for the status page. Everything personal is either
// masked or omitted.
type pageData struct {
	Mode         string
	Uptime       string
	PollInterval string
	Ready        bool
	Failures     int

	AccountSet bool
	AccountID  string // masked to the last four digits
	Currency   string

	Metrics     Metrics
	ReturnPct   string
	LastUpdated string

	// TickersCSV is every held ticker, comma-joined, so the page can offer a
	// TICKERS value that is ready to paste into .env or the Deployment env.
	TickersCSV string
}

var statusTemplate = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>trading212-mqtt</title>
<style>
 body { font-family: system-ui, sans-serif; margin: 2rem; max-width: 40rem; }
 h1 { font-size: 1.25rem; }
 table { border-collapse: collapse; width: 100%; margin-bottom: 1.5rem; }
 th, td { text-align: left; padding: 0.35rem 0.75rem 0.35rem 0; border-bottom: 1px solid #ddd; }
 th { font-weight: 600; width: 45%; }
 .holdings th, .holdings td { width: auto; }
 .holdings td.num { text-align: right; }
 .tickers { margin-bottom: 1.5rem; }
 .tickers code { background: #f3f3f3; padding: 0.2rem 0.4rem; word-break: break-all; }
 .ok { color: #0a7d29; } .bad { color: #b00020; }
</style>
</head>
<body>
<h1>trading212-mqtt</h1>

<table>
 <tr><th>Mode</th><td>{{ .Mode }}</td></tr>
 <tr><th>Readiness</th><td>{{ if .Ready }}<span class="ok">ready</span>{{ else }}<span class="bad">not ready</span>{{ end }}</td></tr>
 <tr><th>Consecutive failures</th><td>{{ .Failures }}</td></tr>
 <tr><th>Poll interval</th><td>{{ .PollInterval }}</td></tr>
 <tr><th>Uptime</th><td>{{ .Uptime }}</td></tr>
 <tr><th>Account</th><td>{{ if .AccountSet }}…{{ .AccountID }} ({{ .Currency }}){{ else }}initialising{{ end }}</td></tr>
</table>

{{ if .Metrics.Set }}
<table>
 <tr><th>Total value</th><td>{{ printf "%.2f" .Metrics.TotalValue }} {{ .Metrics.Currency }}</td></tr>
 <tr><th>Free cash</th><td>{{ printf "%.2f" .Metrics.FreeCash }} {{ .Metrics.Currency }}</td></tr>
 <tr><th>Invested</th><td>{{ printf "%.2f" .Metrics.Invested }} {{ .Metrics.Currency }}</td></tr>
 <tr><th>Unrealised P/L</th><td>{{ printf "%.2f" .Metrics.UnrealizedPL }} {{ .Metrics.Currency }}</td></tr>
 <tr><th>Return</th><td>{{ .ReturnPct }}</td></tr>
 <tr><th>Positions</th><td>{{ .Metrics.TrackedCount }} tracked of {{ .Metrics.PositionCount }} held</td></tr>
 <tr><th>Last updated</th><td>{{ .LastUpdated }}</td></tr>
</table>

{{ if .Metrics.Positions }}
<table class="holdings">
 <tr><th>Ticker</th><th>Name</th><th>Value</th><th>Tracked</th></tr>
 {{ range .Metrics.Positions }}
 <tr><td>{{ .Ticker }}</td><td>{{ .Name }}</td><td class="num">{{ printf "%.2f" .Value }} {{ $.Metrics.Currency }}</td><td>{{ if .Tracked }}yes{{ else }}no{{ end }}</td></tr>
 {{ end }}
</table>
<p class="tickers">Track these in Home Assistant with <code>TICKERS={{ .TickersCSV }}</code></p>
{{ end }}
{{ else }}
<p>Waiting for the first poll — initialising.</p>
{{ end }}
</body>
</html>
`))

func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	d := pageData{
		Mode:         s.cfg.Mode,
		Uptime:       time.Since(s.startedAt).Truncate(time.Second).String(),
		PollInterval: s.cfg.PollInterval.String(),
		Ready:        s.ready,
		Failures:     s.consecutiveFailures,
		AccountSet:   s.accountSet,
		AccountID:    maskAccountID(s.accountID),
		Currency:     s.currency,
		Metrics:      s.metrics,
	}
	s.mu.Unlock()

	d.ReturnPct = "unknown"
	if d.Metrics.ReturnPct != nil {
		d.ReturnPct = strconv.FormatFloat(*d.Metrics.ReturnPct, 'f', 2, 64) + " %"
	}
	if len(d.Metrics.Positions) > 0 {
		tickers := make([]string, 0, len(d.Metrics.Positions))
		for _, p := range d.Metrics.Positions {
			tickers = append(tickers, p.Ticker)
		}
		d.TickersCSV = strings.Join(tickers, ",")
	}
	if !d.Metrics.LastUpdated.IsZero() {
		d.LastUpdated = d.Metrics.LastUpdated.Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = statusTemplate.Execute(w, d)
}

// maskAccountID renders only the last four digits: the account number is
// personal and the page is not authenticated.
func maskAccountID(id int64) string {
	s := strconv.FormatInt(id, 10)
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}
