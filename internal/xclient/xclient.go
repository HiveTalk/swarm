// Package xclient implements a minimal OAuth 1.0a (RFC 5849) client for the
// X/Twitter API, shared between the swarm relay (local mode) and the xpost
// crossposting service. HMAC-SHA1 request signing is ~60 lines and the X
// endpoints used (request_token, access_token, POST /2/tweets) only need
// Authorization headers, not a full client library.
package xclient

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	RequestTokenURL = "https://api.twitter.com/oauth/request_token"
	AuthorizeURL    = "https://api.twitter.com/oauth/authorize"
	AccessTokenURL  = "https://api.twitter.com/oauth/access_token"
	CreateTweetURL  = "https://api.twitter.com/2/tweets"
	UploadMediaURL  = "https://upload.twitter.com/1.1/media/upload.json"
)

// maxMediaBytes caps the media we will download and push through the chunked
// upload. X allows 512MB for video; we stay well under that to keep memory
// sane — larger files fall back to a plain link in the tweet.
const maxMediaBytes = 100 << 20 // 100 MB

// mediaChunkSize must be <= 5MB per APPEND call.
const mediaChunkSize = 4 << 20 // 4 MB

// percentEncode implements RFC 5849 percent-encoding: unreserved characters
// (A-Z a-z 0-9 - . _ ~) pass through, everything else becomes uppercase %XX.
// Go's url.QueryEscape is not equivalent (escapes differently for '+', '~').
func percentEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// authHeader builds the Authorization header value for an OAuth 1.0a request.
// extraParams are non-oauth parameters included in the signature base (e.g.
// oauth_callback, oauth_verifier) — for JSON-bodied requests the body is NOT
// signed per RFC 5849 §3.4.1.3.
func authHeader(method, rawURL string, oauthParams, extraParams map[string]string, signingSecret string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	sigParams := make(map[string]string)
	for k, vs := range u.Query() {
		if len(vs) > 0 {
			sigParams[k] = vs[0]
		}
	}
	for k, v := range oauthParams {
		sigParams[k] = v
	}
	for k, v := range extraParams {
		sigParams[k] = v
	}

	keys := make([]string, 0, len(sigParams))
	for k := range sigParams {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var paramPairs []string
	for _, k := range keys {
		paramPairs = append(paramPairs, percentEncode(k)+"="+percentEncode(sigParams[k]))
	}
	paramString := strings.Join(paramPairs, "&")

	baseURL := u.Scheme + "://" + u.Host + u.Path
	baseString := strings.ToUpper(method) + "&" + percentEncode(baseURL) + "&" + percentEncode(paramString)

	mac := hmac.New(sha1.New, []byte(signingSecret))
	mac.Write([]byte(baseString))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	headerParams := make(map[string]string, len(oauthParams)+len(extraParams)+1)
	for k, v := range oauthParams {
		headerParams[k] = v
	}
	for k, v := range extraParams {
		headerParams[k] = v
	}
	headerParams["oauth_signature"] = signature

	headerKeys := make([]string, 0, len(headerParams))
	for k := range headerParams {
		headerKeys = append(headerKeys, k)
	}
	sort.Strings(headerKeys)

	var parts []string
	for _, k := range headerKeys {
		parts = append(parts, percentEncode(k)+`="`+percentEncode(headerParams[k])+`"`)
	}
	return "OAuth " + strings.Join(parts, ", "), nil
}

func nonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Client performs the 3-legged OAuth 1.0a flow and signs API calls with
// app-level consumer credentials + optional user-context tokens.
type Client struct {
	consumerKey    string
	consumerSecret string
	httpClient     *http.Client
}

func New(consumerKey, consumerSecret string) *Client {
	return &Client{
		consumerKey:    consumerKey,
		consumerSecret: consumerSecret,
		httpClient:     &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) baseParams() (map[string]string, error) {
	n, err := nonce()
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"oauth_consumer_key":     c.consumerKey,
		"oauth_nonce":            n,
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        fmt.Sprintf("%d", time.Now().Unix()),
		"oauth_version":          "1.0",
	}, nil
}

// RequestToken performs step 1 of 3-legged OAuth: returns the request token
// and its secret (needed again in step 3, it's not the token itself).
func (c *Client) RequestToken(ctx context.Context, callbackURL string) (token, secret string, err error) {
	params, err := c.baseParams()
	if err != nil {
		return "", "", err
	}
	extra := map[string]string{"oauth_callback": callbackURL}
	signingSecret := percentEncode(c.consumerSecret) + "&"

	header, err := authHeader("POST", RequestTokenURL, params, extra, signingSecret)
	if err != nil {
		return "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", RequestTokenURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", header)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("request_token failed: %s: %s", resp.Status, string(body))
	}

	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return "", "", fmt.Errorf("parsing request_token response: %w", err)
	}
	if vals.Get("oauth_callback_confirmed") != "true" {
		return "", "", fmt.Errorf("callback URL not confirmed — check the app's Callback URI settings in the X developer portal")
	}
	return vals.Get("oauth_token"), vals.Get("oauth_token_secret"), nil
}

// AccessToken performs step 3: exchanges the verifier for user tokens.
func (c *Client) AccessToken(ctx context.Context, reqToken, reqTokenSecret, verifier string) (token, secret, userID, screenName string, err error) {
	params, err := c.baseParams()
	if err != nil {
		return "", "", "", "", err
	}
	params["oauth_token"] = reqToken
	extra := map[string]string{"oauth_verifier": verifier}
	signingSecret := percentEncode(c.consumerSecret) + "&" + percentEncode(reqTokenSecret)

	header, err := authHeader("POST", AccessTokenURL, params, extra, signingSecret)
	if err != nil {
		return "", "", "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", AccessTokenURL, nil)
	if err != nil {
		return "", "", "", "", err
	}
	req.Header.Set("Authorization", header)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", "", "", fmt.Errorf("access_token failed: %s: %s", resp.Status, string(body))
	}

	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return "", "", "", "", fmt.Errorf("parsing access_token response: %w", err)
	}
	return vals.Get("oauth_token"), vals.Get("oauth_token_secret"), vals.Get("user_id"), vals.Get("screen_name"), nil
}

// PostTweet sends a user-context signed POST /2/tweets.
func (c *Client) PostTweet(ctx context.Context, accessToken, accessSecret, text string) (tweetID string, err error) {
	return c.PostTweetWithMedia(ctx, accessToken, accessSecret, text, nil)
}

// PostTweetWithMedia is PostTweet with uploaded media_ids attached (e.g. a
// video from UploadMedia). An empty mediaIDs posts plain text.
func (c *Client) PostTweetWithMedia(ctx context.Context, accessToken, accessSecret, text string, mediaIDs []string) (tweetID string, err error) {
	params, err := c.baseParams()
	if err != nil {
		return "", err
	}
	params["oauth_token"] = accessToken
	// JSON body is excluded from the signature per RFC 5849.
	signingSecret := percentEncode(c.consumerSecret) + "&" + percentEncode(accessSecret)

	header, err := authHeader("POST", CreateTweetURL, params, nil, signingSecret)
	if err != nil {
		return "", err
	}

	tweet := map[string]interface{}{"text": text}
	if len(mediaIDs) > 0 {
		tweet["media"] = map[string][]string{"media_ids": mediaIDs}
	}
	bodyBytes, err := json.Marshal(tweet)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", CreateTweetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", header)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("create tweet failed: %s: %s", resp.Status, string(respBody))
	}

	var parsed struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &parsed); err == nil {
		return parsed.Data.ID, nil
	}
	return "", nil
}

// signedUpload issues a signed request to the v1.1 media upload endpoint.
// command params travel in the query string (included in the signature base);
// multipart bodies are excluded from the signature per RFC 5849.
func (c *Client) signedUpload(ctx context.Context, method string, query url.Values, body io.Reader, contentType, accessToken, accessSecret string) ([]byte, error) {
	params, err := c.baseParams()
	if err != nil {
		return nil, err
	}
	params["oauth_token"] = accessToken
	signingSecret := percentEncode(c.consumerSecret) + "&" + percentEncode(accessSecret)

	rawURL := UploadMediaURL + "?" + query.Encode()
	header, err := authHeader(method, rawURL, params, nil, signingSecret)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", header)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("media upload %s failed: %s: %s", query.Get("command"), resp.Status, string(respBody))
	}
	return respBody, nil
}

// IsPublicHTTPURL reports whether raw is an http(s) URL whose host does not
// resolve to a loopback, private, link-local, or unspecified address. Used to
// blunt SSRF before server-side fetches of caller-supplied URLs (media
// downloads, delegate notify targets).
func IsPublicHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		return isPublicIP(ip)
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return false
		}
	}
	return true
}

func isPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified())
}

// UploadMedia downloads mediaURL (video) and uploads it to X via the chunked
// INIT → APPEND → FINALIZE → STATUS flow, returning the media_id for use in
// PostTweetWithMedia. Caller should fall back to a plain-text post on error.
func (c *Client) UploadMedia(ctx context.Context, accessToken, accessSecret, mediaURL string) (mediaID string, err error) {
	if !IsPublicHTTPURL(mediaURL) {
		return "", fmt.Errorf("media URL must be a public http(s) URL")
	}
	// Download the source media (cap enforced via LimitReader+1).
	getReq, err := http.NewRequestWithContext(ctx, "GET", mediaURL, nil)
	if err != nil {
		return "", err
	}
	dlClient := &http.Client{Timeout: 3 * time.Minute}
	dlResp, err := dlClient.Do(getReq)
	if err != nil {
		return "", fmt.Errorf("downloading media: %w", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading media: %s", dlResp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(dlResp.Body, maxMediaBytes+1))
	if err != nil {
		return "", fmt.Errorf("downloading media: %w", err)
	}
	if len(data) > maxMediaBytes {
		return "", fmt.Errorf("media exceeds %dMB limit", maxMediaBytes>>20)
	}

	mediaType := strings.SplitN(dlResp.Header.Get("Content-Type"), ";", 2)[0]
	if !strings.HasPrefix(mediaType, "video/") {
		mediaType = "video/mp4"
	}

	// INIT
	respBody, err := c.signedUpload(ctx, "POST", url.Values{
		"command":        {"INIT"},
		"total_bytes":    {fmt.Sprintf("%d", len(data))},
		"media_type":     {mediaType},
		"media_category": {"tweet_video"},
	}, nil, "", accessToken, accessSecret)
	if err != nil {
		return "", err
	}
	var initResp struct {
		MediaIDString string `json:"media_id_string"`
	}
	if err := json.Unmarshal(respBody, &initResp); err != nil || initResp.MediaIDString == "" {
		return "", fmt.Errorf("INIT returned no media_id: %s", string(respBody))
	}
	mediaID = initResp.MediaIDString

	// APPEND chunks
	for idx, off := 0, 0; off < len(data); idx, off = idx+1, off+mediaChunkSize {
		end := off + mediaChunkSize
		if end > len(data) {
			end = len(data)
		}
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		part, err := w.CreateFormFile("media", "chunk")
		if err != nil {
			return "", err
		}
		if _, err := part.Write(data[off:end]); err != nil {
			return "", err
		}
		w.Close()

		// APPEND returns 2xx with empty body on success.
		if _, err := c.signedUpload(ctx, "POST", url.Values{
			"command":       {"APPEND"},
			"media_id":      {mediaID},
			"segment_index": {fmt.Sprintf("%d", idx)},
		}, &buf, w.FormDataContentType(), accessToken, accessSecret); err != nil {
			return "", err
		}
	}

	// FINALIZE
	respBody, err = c.signedUpload(ctx, "POST", url.Values{
		"command":  {"FINALIZE"},
		"media_id": {mediaID},
	}, nil, "", accessToken, accessSecret)
	if err != nil {
		return "", err
	}

	// Video transcoding is async — poll STATUS until succeeded/failed.
	type statusResp struct {
		ProcessingInfo *struct {
			State           string `json:"state"`
			CheckAfterSecs  int    `json:"check_after_secs"`
			ProgressPercent int    `json:"progress_percent"`
			Error           *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"processing_info"`
	}
	var fin statusResp
	_ = json.Unmarshal(respBody, &fin)
	for fin.ProcessingInfo != nil &&
		(fin.ProcessingInfo.State == "pending" || fin.ProcessingInfo.State == "in_progress") {
		wait := time.Duration(fin.ProcessingInfo.CheckAfterSecs) * time.Second
		if wait < time.Second {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
		respBody, err = c.signedUpload(ctx, "GET", url.Values{
			"command":  {"STATUS"},
			"media_id": {mediaID},
		}, nil, "", accessToken, accessSecret)
		if err != nil {
			return "", err
		}
		fin = statusResp{}
		_ = json.Unmarshal(respBody, &fin)
	}
	if fin.ProcessingInfo != nil && fin.ProcessingInfo.State == "failed" {
		msg := "processing failed"
		if fin.ProcessingInfo.Error != nil {
			msg = fin.ProcessingInfo.Error.Message
		}
		return "", fmt.Errorf("video processing: %s", msg)
	}
	return mediaID, nil
}

// PostViaService asks a delegate xpost service to tweet on behalf of a linked
// account, authenticating with the per-link bearer token issued at link time.
// textMedia is the body to use if mediaURL uploads successfully (the media
// URL stripped); on upload failure the service posts text with the URL kept.
func PostViaService(ctx context.Context, serviceURL, linkToken, pubkey, eventID, text, textMedia, mediaURL string) (tweetID string, err error) {
	payload, _ := json.Marshal(map[string]string{
		"pubkey":     pubkey,
		"event_id":   eventID,
		"text":       text,
		"text_media": textMedia,
		"media_url":  mediaURL,
	})

	endpoint := strings.TrimRight(serviceURL, "/") + "/v1/post"
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+linkToken)
	req.Header.Set("Content-Type", "application/json")

	// Generous timeout: media download + chunked upload + transcode polling
	// can take a couple of minutes for large clips.
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("service post failed: %s: %s", resp.Status, string(body))
	}

	var parsed struct {
		TweetID string `json:"tweet_id"`
	}
	_ = json.Unmarshal(body, &parsed)
	return parsed.TweetID, nil
}
