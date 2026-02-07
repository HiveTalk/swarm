package main

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/fiatjaf/khatru"
)

const frontPageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.RelayName}} - Nostr Relay & Blossom Server</title>

    <!-- Open Graph / Link Preview Meta Tags -->
    <meta property="og:type" content="website">
    <meta property="og:title" content="{{.RelayName}} - Nostr Relay & Blossom Server">
    <meta property="og:description" content="{{.RelayDescription}} - Team-based Nostr relay with Blossom file storage">
    <meta property="og:image" content="https://swarm.hivetalk.org/public/TeamHive.png">
    <meta property="og:url" content="https://{{.TeamDomain}}">

    <!-- Twitter Card Meta Tags -->
    <meta name="twitter:card" content="summary">
    <meta name="twitter:title" content="{{.RelayName}} - Nostr Relay & Blossom Server">
    <meta name="twitter:description" content="{{.RelayDescription}} - Team-based Nostr relay with Blossom file storage">
    <meta name="twitter:image" content="https://swarm.hivetalk.org/public/TeamHive.png">

    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }

        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
            line-height: 1.6;
            color: #333;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
        }

        .container {
            max-width: 1200px;
            margin: 0 auto;
            padding: 2rem;
        }

        .header {
            text-align: center;
            color: white;
            margin-bottom: 3rem;
        }

        .header-content {
            display: flex;
            align-items: center;
            justify-content: center;
            gap: 1rem;
        }

        .header-logo {
            width: 100px;
            height: 100px;
            object-fit: contain;
        }

        .header h1 {
            font-size: 3rem;
            margin-bottom: 0.5rem;
            text-shadow: 2px 2px 4px rgba(0,0,0,0.3);
        }

        .header p {
            font-size: 1.2rem;
            opacity: 0.9;
        }

        .card {
            background: white;
            border-radius: 12px;
            padding: 2rem;
            margin-bottom: 2rem;
            box-shadow: 0 10px 30px rgba(0,0,0,0.1);
            transition: transform 0.3s ease;
        }

        .card:hover {
            transform: translateY(-5px);
        }

        .card h2 {
            color: #4a5568;
            margin-bottom: 1rem;
            font-size: 1.5rem;
            border-bottom: 2px solid #e2e8f0;
            padding-bottom: 0.5rem;
        }

        .endpoint {
            margin-bottom: 1.5rem;
            padding: 1rem;
            background: #f7fafc;
            border-radius: 8px;
            border-left: 4px solid #667eea;
        }

        .endpoint-title {
            font-weight: bold;
            color: #2d3748;
            margin-bottom: 0.5rem;
        }

        .method {
            display: inline-block;
            padding: 0.2rem 0.5rem;
            border-radius: 4px;
            font-size: 0.8rem;
            font-weight: bold;
            margin-right: 0.5rem;
        }

        .method.get { background: #48bb78; color: white; }
        .method.post { background: #ed8936; color: white; }
        .method.put { background: #4299e1; color: white; }
        .method.delete { background: #e53e3e; color: white; }
        .method.websocket { background: #805ad5; color: white; }

        .path {
            font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
            background: #2d3748;
            color: #e2e8f0;
            padding: 0.2rem 0.5rem;
            border-radius: 4px;
            font-size: 0.9rem;
        }

        .description {
            color: #4a5568;
            margin-top: 0.5rem;
        }

        .status-info {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
            gap: 1rem;
            margin-top: 1rem;
        }

        .status-item {
            background: #edf2f7;
            padding: 1rem;
            border-radius: 8px;
            text-align: center;
        }

        .status-label {
            font-size: 0.8rem;
            color: #718096;
            text-transform: uppercase;
            letter-spacing: 0.05em;
        }

        .status-value {
            font-size: 1.2rem;
            font-weight: bold;
            color: #2d3748;
            margin-top: 0.25rem;
        }

        .footer {
            text-align: center;
            color: white;
            opacity: 0.8;
            margin-top: 3rem;
        }

        .footer a {
            color: white;
            text-decoration: none;
            border-bottom: 1px solid rgba(255,255,255,0.3);
        }

        .footer a:hover {
            border-bottom-color: white;
        }

        @media (max-width: 768px) {
            .container {
                padding: 1rem;
            }

            .header h1 {
                font-size: 2rem;
            }

            .card {
                padding: 1.5rem;
            }
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <div class="header-content">
                <img src="/public/TeamHive.png" alt="TeamHive Logo" class="header-logo">
                <h1>{{.RelayName}}</h1>
            </div>
            <p>{{.RelayDescription}}</p>
        </div>

                {{if .BlossomEnabled}}
        <div class="card">
            <h2>🌺 Bouquet Client</h2>

            <div class="endpoint">
                <div class="endpoint-title">
                    <span class="method get">WEB APP</span>
                    <span class="path">/bouquet/</span>
                </div>
                <div class="description">
                    Modern web interface for managing your blobs. Upload files, organize content,
                    and publish to Nostr with rich metadata support including audio, video, and images.
                </div>
                <div style="margin-top: 1rem; text-align: center;">
                    <a href="/bouquet/" target="_blank" class="btn" style="background: #be185d; color: white; text-decoration: none; padding: 0.75rem 1.5rem; border-radius: 8px; display: inline-block; font-weight: bold;">
                        🚀 Launch Bouquet Client
                    </a>
                </div>
            </div>

        </div>
        {{end}}

        <div class="card">
            <h2>📋 Curator</h2>
            <div class="endpoint">
                <div class="endpoint-title">
                    <span class="method get">WEB APP</span>
                    <span class="path">https://curator.hivetalk.org</span>
                </div>
                <div class="description">
                    Simple interface to quickly lookup any kind on a relay.
                    Login to switch relays, view json, post kind 1 and delete notes.
                </div>
                <div style="margin-top: 1rem; text-align: center;">
                    <a href="https://curator.hivetalk.org" target="_blank" class="btn" style="background: #1844beff; color: white; text-decoration: none; padding: 0.75rem 1.5rem; border-radius: 8px; display: inline-block; font-weight: bold;">
                        🚀 Launch Curator Client
                    </a>
                </div>
            </div>
        </div>


        <div class="card">
            <h2>🔗 Nostr Relay Endpoints</h2>

            <div class="endpoint">
                <div class="endpoint-title">
                    <span class="method websocket">WebSocket</span>
                    <span class="path">{{.WebSocketURL}}</span>
                </div>
                <div class="description">
                    Main Nostr relay WebSocket endpoint for publishing and subscribing to events.
                    Supports standard Nostr protocol (NIP-01) with team-based access control.
                </div>
            </div>

            <div class="endpoint">
                <div class="endpoint-title">
                    <span class="method get">GET</span>
                    <span class="path">{{.WellKnownURL}}</span>
                </div>
                <div class="description">
                    Nostr relay information document (NIP-11) containing relay metadata and policies.
                </div>
            </div>
        </div>

        <div class="card">
            <h2>📅 Scheduled Notes API</h2>

            <div class="endpoint">
                <div class="endpoint-title">
                    <span class="method post">POST</span>
                    <span class="path">/api/scheduler/schedule</span>
                </div>
                <div class="description">
                    Schedule a signed Nostr event for future publication. Requires NIP-98 authentication.
                    Send a JSON body with <code>signed_event</code>, <code>relays</code>, and <code>scheduled_for</code> (ISO 8601 timestamp).
                </div>
            </div>

            <div class="endpoint">
                <div class="endpoint-title">
                    <span class="method get">GET</span>
                    <span class="path">/api/scheduler/list</span>
                </div>
                <div class="description">
                    List all scheduled posts for the authenticated user. Requires NIP-98 authentication.
                    Returns an array of scheduled posts with their status (pending, published, or failed).
                </div>
            </div>

            <div class="endpoint">
                <div class="endpoint-title">
                    <span class="method delete">DELETE</span>
                    <span class="path">/api/scheduler/delete?id={id}</span>
                </div>
                <div class="description">
                    Delete a pending scheduled post by its ID. Requires NIP-98 authentication.
                    Only the user who created the scheduled post can delete it.
                </div>
            </div>
        </div>

        {{if .BlossomEnabled}}
        <div class="card">
            <h2>🌸 Blossom Server Endpoints</h2>

            <div class="endpoint">
                <div class="endpoint-title">
                    <span class="method get">GET</span>
                    <span class="path">/{sha256}</span>
                </div>
                <div class="description">
                    Download a blob by its SHA256 hash. Returns the raw file content with appropriate MIME type.
                </div>
            </div>

            <div class="endpoint">
                <div class="endpoint-title">
                    <span class="method put">PUT</span>
                    <span class="path">/upload</span>
                </div>
                <div class="description">
                    Upload a new blob to the server. Requires Nostr event authentication (NIP-98).
                    Maximum file size: {{.MaxUploadSizeMB}}MB.
                </div>
            </div>

            <div class="endpoint">
                <div class="endpoint-title">
                    <span class="method get">GET</span>
                    <span class="path">/list/{pubkey}</span>
                </div>
                <div class="description">
                    List all blobs with metadata including SHA256, size, MIME type, and upload timestamp.
                    Used for health checks and blob discovery.
                </div>
            </div>

            <div class="endpoint">
                <div class="endpoint-title">
                    <span class="method put">PUT</span>
                    <span class="path">/mirror</span>
                </div>
                <div class="description">
                    Mirror a blob from another Blossom server. Accepts JSON body with source URL,
                    downloads and verifies the blob, then stores it locally.
                </div>
            </div>
        </div>
        {{end}}

        <div class="card">
            <h2>📊 Server Status</h2>
            <div class="status-info">
                <div class="status-item">
                    <div class="status-label">Team Domain</div>
                    <div class="status-value">{{.TeamDomain}}</div>
                </div>
                {{if .BlossomEnabled}}
                <div class="status-item">
                    <div class="status-label">Blossom URL</div>
                    <div class="status-value">{{.BlossomURL}}</div>
                </div>
                <div class="status-item">
                    <div class="status-label">Max Upload Size</div>
                    <div class="status-value">{{.MaxUploadSizeMB}}MB</div>
                </div>
                {{end}}
                <div class="status-item">
                    <div class="status-label">Access Control</div>
                    <div class="status-value">Team Members Only</div>
                    <div class="status-detail"><a href="/dashboard"  class="btn" 
                    style="background: #2e6fd0ff; color: white; text-decoration: none; padding: 0.75rem 1.5rem; border-radius: 8px; display: inline-block; font-weight: bold;">
                    Dashboard</a>
                    </div>
                </div>
                {{if .AllowedKindsStr}}
                <div class="status-item">
                    <div class="status-label">Allowed Event Kinds</div>
                    <div class="status-value">{{.AllowedKindsStr}}</div>
                </div>
                {{end}}
            </div>
        </div>

        <div class="footer">
            <p>
                Powered by <a href="https://khatru.nostr.technology/" target="_blank">Khatru</a>
                {{if .BlossomEnabled}}& <a href="https://khatru.nostr.technology/core/blossom" target="_blank">Blossom</a>{{end}}
                | Source Code <a href="https://github.com/HiveTalk/swarm" target="_blank">on GitHub</a>
            </p>
        </div>
    </div>
</body>
</html>`

type FrontPageData struct {
	RelayName        string
	RelayDescription string
	TeamDomain       string
	BlossomEnabled   bool
	BlossomURL       string
	MaxUploadSizeMB  int
	AllowedKindsStr  string
	WebSocketURL     string
	WellKnownURL     string
}

func setupFrontPageHandler(relay *khatru.Relay, config Config) {
	relay.Router().HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Only serve the front page for GET requests to the root path
		if r.Method != "GET" || r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		// Check if this is a WebSocket upgrade request
		if strings.ToLower(r.Header.Get("Upgrade")) == "websocket" {
			// Let the relay handle WebSocket connections
			relay.ServeHTTP(w, r)
			return
		}

		// Prepare template data
		data := FrontPageData{
			RelayName:        config.RelayName,
			RelayDescription: config.RelayDescription,
			TeamDomain:       config.TeamDomain,
			BlossomEnabled:   config.BlossomEnabled,
			MaxUploadSizeMB:  config.MaxUploadSizeMB,
			WebSocketURL:     *config.WebSocketURL,
			WellKnownURL:     "https://" + config.NPUBDomain + "/public/.well-known/nostr.json",
		}

		if config.BlossomURL != nil {
			data.BlossomURL = *config.BlossomURL
		}

		// Format allowed kinds for display
		if len(config.AllowedKinds) > 0 {
			kindStrs := make([]string, len(config.AllowedKinds))
			for i, kind := range config.AllowedKinds {
				kindStrs[i] = strconv.Itoa(kind)
			}
			data.AllowedKindsStr = strings.Join(kindStrs, ", ")
		}

		// Parse and execute template
		tmpl, err := template.New("frontpage").Parse(frontPageTemplate)
		if err != nil {
			http.Error(w, "Template error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, data); err != nil {
			http.Error(w, "Template execution error", http.StatusInternalServerError)
			return
		}
	})
}
