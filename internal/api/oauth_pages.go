package api

import "html/template"

const pageHead = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>Innkeeper</title>
<style>
:root{color-scheme:light dark;--bg:#faf8f5;--fg:#1f1b16;--muted:#6b6258;--line:#ddd5ca;--accent:#7a4a1e}
@media (prefers-color-scheme:dark){:root{--bg:#1a1714;--fg:#f0ebe4;--muted:#a59a8d;--line:#3a332c;--accent:#e0a96d}}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,sans-serif}
main{max-width:28rem;margin:10vh auto;padding:0 16px}
h1{font-size:1.4rem;margin:0 0 1rem}
p,label{color:var(--muted)}
fieldset{border:1px solid var(--line);border-radius:8px;margin:1rem 0;padding:.5rem 1rem}
label{display:block;margin:.5rem 0}
label b{color:var(--fg)}
input[type=text],input[type=password]{width:100%;box-sizing:border-box;padding:.6rem;border:1px solid var(--line);border-radius:6px;background:transparent;color:inherit;font:inherit}
button,a.button{display:inline-block;padding:.6rem 1.2rem;border-radius:6px;border:1px solid var(--line);background:transparent;color:inherit;font:inherit;text-decoration:none;cursor:pointer;margin:.5rem .5rem 0 0}
button.primary{background:var(--accent);border-color:var(--accent);color:var(--bg)}
.error{color:#c0392b}
</style></head><body><main>`

const pageFoot = `</main></body></html>`

var (
	errorPage = template.Must(template.New("error").Parse(pageHead +
		`<h1>Can't connect</h1><p>{{.}}</p>` + pageFoot))

	providersPage = template.Must(template.New("providers").Parse(pageHead +
		`<h1>Log in to Innkeeper</h1>{{if .Failed}}<p class="error">{{.Failed}}</p>{{end}}
{{range .Providers}}<a class="button" href="/oauth/login?provider={{.ID}}&amp;return={{$.Return}}">{{.Name}}</a>{{end}}` + pageFoot))

	loginPage = template.Must(template.New("login").Parse(pageHead +
		`<h1>Log in to Innkeeper</h1>{{if .Failed}}<p class="error">{{.Failed}}</p>{{end}}
<form method="post" action="/oauth/login">
<input type="hidden" name="provider" value="{{.Provider}}"><input type="hidden" name="return" value="{{.Return}}">
{{range .Fields}}<label>{{.Title}}<input type="{{.Type}}" name="f_{{.Name}}" required autofocus></label>{{end}}
<button class="primary">Log in</button></form>` + pageFoot))

	consentPage = template.Must(template.New("consent").Parse(pageHead +
		`<h1>Connect {{.Client}}?</h1>
<p><b>{{.Client}}</b> wants to use your Innkeeper library. After you approve, you go back to <b>{{.Host}}</b>.</p>
<form method="post" action="/oauth/authorize">
{{range $k, $v := .Params}}<input type="hidden" name="{{$k}}" value="{{index $v 0}}">{{end}}
<input type="hidden" name="csrf" value="{{.CSRF}}">
<fieldset><legend>What may it do?</legend>
<label><input type="radio" name="tier" value="read" checked> <b>Read</b>: search and read cards, lorebooks, and sync status</label>
<label><input type="radio" name="tier" value="write"> <b>Write</b>: also edit the library, which revision history can undo</label>
<label><input type="radio" name="tier" value="destructive"> <b>Destructive</b>: also push to apps, overwriting their copies, and change plugin settings</label>
</fieldset>
<button class="primary" name="decision" value="approve">Approve</button><button name="decision" value="deny">Deny</button>
</form>` + pageFoot))
)
