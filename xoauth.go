package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Minimal OAuth 1.0a (RFC 5849) client for the X/Twitter API. Kept dependency-
// free: HMAC-SHA1 request signing is ~60 lines and the X endpoints we use
// (request_token, access_token, POST /2/tweets) only need Authorization
// headers, not a full client library.

const (
	xRequestTokenURL = "https://api.twitter.com/oauth/request_token"
	xAuthorizeURL    = "https://api.twitter.com/oauth/authorize"
	xAccessTokenURL  = "https://api.twitter.com/oauth/access_token"
	xCreateTweetURL  = "https://api.twitter.com/2/tweets"
)

// oauthPercentEncode implements RFC 5849 percent-encoding: unreserved
// characters (A-Z a-z 0-9 - . _ ~) pass through, everything else becomes
// uppercase %XX. Go's url.QueryEscape is not equivalent (escapes differently
// for '+', '~', etc).
func oauthPercentEncode(s string) string {
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

// oauth1AuthHeader builds the Authorization header value for an OAuth 1.0a
// request. extraParams are non-oauth parameters that must be included in the
// signature base (e.g. oauth_callback, oauth_verifier) — for JSON-bodied
// requests the body is NOT signed per RFC 5849 §3.4.1.3.
func oauth1AuthHeader(method, rawURL string, oauthParams, extraParams map[string]string, signingSecret string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	// Signature parameters = query params + oauth_* + extra (body params are
	// excluded for JSON payloads).
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
		paramPairs = append(paramPairs, oauthPercentEncode(k)+"="+oauthPercentEncode(sigParams[k]))
	}
	paramString := strings.Join(paramPairs, "&")

	baseURL := u.Scheme + "://" + u.Host + u.Path
	baseString := strings.ToUpper(method) + "&" + oauthPercentEncode(baseURL) + "&" + oauthPercentEncode(paramString)

	mac := hmac.New(sha1.New, []byte(signingSecret))
	mac.Write([]byte(baseString))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	// Header = oauth params + signature (extras like oauth_callback are
	// transmitted in the header too, per X's request_token expectations).
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
		parts = append(parts, oauthPercentEncode(k)+`="`+oauthPercentEncode(headerParams[k])+`"`)
	}
	return "OAuth " + strings.Join(parts, ", "), nil
}

func oauthNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func mustJSONMarshal(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// xOAuthClient performs the 3-legged OAuth 1.0a flow and signs API calls with
// app-level consumer credentials + optional user-context tokens.
type xOAuthClient struct {
	consumerKey    string
	consumerSecret string
	httpClient     *http.Client
}

func newXOAuthClient(consumerKey, consumerSecret string) *xOAuthClient {
	return &xOAuthClient{
		consumerKey:    consumerKey,
		consumerSecret: consumerSecret,
		httpClient:     &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *xOAuthClient) baseParams() (map[string]string, error) {
	nonce, err := oauthNonce()
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"oauth_consumer_key":     c.consumerKey,
		"oauth_nonce":            nonce,
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        fmt.Sprintf("%d", time.Now().Unix()),
		"oauth_version":          "1.0",
	}, nil
}

// requestToken performs step 1 of 3-legged OAuth: returns the request token
// and its secret (needed again in step 3, it's not the token itself).
func (c *xOAuthClient) requestToken(ctx context.Context, callbackURL string) (token, secret string, err error) {
	params, err := c.baseParams()
	if err != nil {
		return "", "", err
	}
	extra := map[string]string{"oauth_callback": callbackURL}
	signingSecret := oauthPercentEncode(c.consumerSecret) + "&"

	header, err := oauth1AuthHeader("POST", xRequestTokenURL, params, extra, signingSecret)
	if err != nil {
		return "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", xRequestTokenURL, nil)
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

// accessToken performs step 3: exchanges the verifier for user tokens.
func (c *xOAuthClient) accessToken(ctx context.Context, reqToken, reqTokenSecret, verifier string) (token, secret, userID, screenName string, err error) {
	params, err := c.baseParams()
	if err != nil {
		return "", "", "", "", err
	}
	params["oauth_token"] = reqToken
	extra := map[string]string{"oauth_verifier": verifier}
	signingSecret := oauthPercentEncode(c.consumerSecret) + "&" + oauthPercentEncode(reqTokenSecret)

	header, err := oauth1AuthHeader("POST", xAccessTokenURL, params, extra, signingSecret)
	if err != nil {
		return "", "", "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", xAccessTokenURL, nil)
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

// postTweet sends a user-context signed POST /2/tweets.
func (c *xOAuthClient) postTweet(ctx context.Context, acct *XAccount, text string) (tweetID string, err error) {
	params, err := c.baseParams()
	if err != nil {
		return "", err
	}
	params["oauth_token"] = acct.AccessToken
	// JSON body is excluded from the signature per RFC 5849.
	signingSecret := oauthPercentEncode(c.consumerSecret) + "&" + oauthPercentEncode(acct.AccessSecret)

	header, err := oauth1AuthHeader("POST", xCreateTweetURL, params, nil, signingSecret)
	if err != nil {
		return "", err
	}

	body := fmt.Sprintf(`{"text":%s}`, mustJSONMarshal(text))
	req, err := http.NewRequestWithContext(ctx, "POST", xCreateTweetURL, strings.NewReader(body))
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
