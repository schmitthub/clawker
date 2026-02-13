package hostproxy

import (
	"fmt"
	"html"
)

// ─── Callback page HTML ────────────────────────────────────────────

// callbackPage renders a full HTML document with the shared callback chrome.
// title sets <title> and body is raw HTML injected inside the centered container.
// Callers are responsible for escaping any user-supplied content in body.
//
// Colors are drawn from the project's semantic theme (internal/iostreams/styles.go).
func callbackPage(title, body string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>%s — Clawker</title>
    <style>
        :root {
            color-scheme: dark;

            /* Semantic theme — mirrors internal/iostreams/styles.go */
            --primary:   #E8714A;  /* ColorBurntOrange */
            --success:   #04B575;  /* ColorEmerald     */
            --error:     #FF5F87;  /* ColorHotPink     */
            --bg:        #1A1A1A;  /* ColorJet         */
            --subtle:    #A0A0A0;  /* ColorSilver      */
        }

        * { margin: 0; padding: 0; box-sizing: border-box; }

        body {
            font-family: "SF Mono", "Cascadia Code", "JetBrains Mono",
                         "Fira Code", "Menlo", "Consolas", monospace;
            display: flex;
            justify-content: center;
            align-items: center;
            min-height: 100vh;
            background: var(--bg);
            color: #F2F2F2;
        }

        .content {
            text-align: center;
            padding: 5vmin;
        }

        h1 {
            font-size: clamp(24px, 4.5vmin, 56px);
            font-weight: 700;
            letter-spacing: -0.02em;
            margin-bottom: 3vmin;
            color: #F2F2F2;
        }

        h1 .ok  { color: var(--success); }
        h1 .err { color: var(--error); }

        .brand {
            text-transform: uppercase;
            letter-spacing: 0.22em;
            font-size: clamp(10px, 1.6vmin, 16px);
            color: var(--primary);
            font-weight: 600;
            margin-top: 4vmin;
        }

        .subtitle {
            color: var(--subtle);
            font-size: clamp(13px, 2.2vmin, 24px);
            line-height: 1.5;
        }
    </style>
</head>
<body>
    <div class="content">
        %s
    </div>
</body>
</html>`, html.EscapeString(title), body)
}

// ─── Body content ───────────────────────────────────────────────────

const callbackSuccessBody = `<h1><span class="ok">&#10004;</span> Authentication Complete</h1>
        <p class="subtitle">&#187; You can close this tab and return to the Clawker container.</p>
        <div class="brand">clawker</div>`

const callbackErrorBodyFmt = `<h1><span class="err">&#10008;</span> Authentication Error</h1>
        <p class="subtitle">&#9646;&#9646; %s</p>
        <div class="brand">clawker</div>`

// callbackErrorBody formats the error body with a safely escaped message.
func callbackErrorBody(message string) string {
	return fmt.Sprintf(callbackErrorBodyFmt, html.EscapeString(message))
}
