# Shared X Crossposting Service (`xpost`)

`xpost` lets every swarm relay offer "Connect your X account" in the CMS
admin panel **without each relay needing its own X developer credentials**.
One `xpost` deployment holds a single X developer app and does the posting;
relays just point at it with `XPOST_SERVICE_URL`.

```
CMS ──▶ swarm relay ──▶ xpost service ──▶ X API
        (no X secrets)    (holds the X app)
```

## Who this guide is for

- **The person running the shared service** — needs an X developer account
  (one-time setup, ~15 minutes). Sections 1–3.
- **A relay operator who wants crossposting** — just sets one variable.
  Section 4.
- **An end user linking their X account** — clicks a button. Section 5.

---

## 1. Create the X developer app (service owner, one time)

1. Go to <https://developer.x.com/> and sign in with the X account that will
   own the app.
2. Create a **project** with an app inside it (standalone apps can't post).
3. In the app's **User authentication settings**:
   - App permissions: **Read and write**
   - Type of App: **Web App, Automated App or Bot**
   - Callback URI: `https://xpost.yourdomain.com/v1/link/callback`
     (use the public hostname you'll run `xpost` on)
   - Website URL: anything valid, e.g. `https://yourdomain.com`
4. On the app's **Keys and tokens** tab, copy the **API Key** and
   **API Key Secret**. These are your `TWITTER_KEY` / `TWITTER_KEY_SECRET`.

> The X **free tier** allows ~500 posts/month and only lets the app post as
> the account that owns the app for write scope — check the current limits on
> the X developer portal for the tier you need. For multi-user linking a paid
> Basic tier is usually required.

## 2. Run the service (service owner)

```bash
cd swarm
go build -o xpost ./cmd/xpost

TWITTER_KEY=... \
TWITTER_KEY_SECRET=... \
XPOST_PUBLIC_URL=https://xpost.yourdomain.com \
PORT=8090 \
XPOST_DB_PATH=/var/lib/xpost \
./xpost
```

Put it behind your usual TLS proxy (nginx/caddy) at
`https://xpost.yourdomain.com`. Verify with `curl https://xpost.yourdomain.com/healthz`
→ `ok`.

That's it. No X secrets leave this box.

## 3. Point a relay at it (relay operator)

Set one variable on each swarm relay and restart:

```env
XPOST_SERVICE_URL=https://xpost.yourdomain.com
```

Do **not** set `TWITTER_KEY`/`TWITTER_KEY_SECRET` on the relay — it doesn't
need them. The relay stores only a per-user link token in
`<DB_PATH>/x_accounts.json`, never an X credential.

## 4. Link an X account (end user)

1. Open the CMS admin panel → **Crossposting** tab.
2. Click **Connect X account** — you're sent to x.com.
3. Click **Authorize app** — you're dropped back on the Crossposting tab,
   now showing your @handle.
4. Leave the toggle on. Every kind-1 note you publish — including scheduled
   notes when they go out — is posted to X automatically.

**What posts look like:** people mentioned via `nostr:npub…`/`nprofile…`
appear as their profile display name; `nostr:note`/`nevent`/`naddr` links
become clickable `nostr.at` links; a `nostr.at` footer links back to the
original note; `.mp4`/`.mov` video links attach as a real playable X video
(if the upload ever fails, the tweet goes out with the plain link instead).

Disconnect or pause any time from the same tab.

## Security notes

- The relay ↔ service link is authenticated by a one-time **nonce** the relay
  generates at connect time (the service can't forge or replay a link for an
  arbitrary pubkey) plus a per-user **link token** the service issues at the
  end of the flow; all `/v1/post` calls carry it as a Bearer token.
- Service-side account data lives in `<XPOST_DB_PATH>/xpost_accounts.json`
  (mode 0600). Treat it like any credential store — back it up, restrict it.
- Anyone who can reach `/v1/link/start` can start *an* OAuth flow, but the
  relay only accepts link completions for nonces it generated — a drive-by
  link can't attach to a user who didn't start it. Still, front the service
  with your normal rate limiting.

## Troubleshooting

| Symptom | Likely cause |
|---------|--------------|
| "X crossposting is not configured" in the CMS | `XPOST_SERVICE_URL` (or `TWITTER_KEY*`) not set on the relay |
| Connect redirects to an error page | `XPOST_PUBLIC_URL` doesn't match the hostname users hit, or the Callback URI in the X portal isn't `<public-url>/v1/link/callback` |
| Linked but posts never arrive | X app tier/permissions — check the service logs; the relay also records `last_error` per account (visible in the Crossposting tab) |
| `/v1/post` returns 401 | The link token is stale (user disconnected on the service side) — disconnect and reconnect from the CMS |
