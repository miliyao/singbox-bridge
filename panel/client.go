package panel

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	httpTimeout          = 30 * time.Second
	requestMaxAttempts   = 3
	requestRetryBaseWait = 100 * time.Millisecond
)

// Client is a minimal HTTP client for the Xboard UniProxy endpoints.
type Client struct {
	baseURL    string
	token      string
	nodeID     int
	httpClient *http.Client
	userETag   string
	configETag string
	config     *NodeConfig
	users      []User
}

type NodeConfig struct {
	Protocol    string          `json:"protocol"`
	ListenIP    string          `json:"listen_ip"`
	ServerPort  int             `json:"server_port"`
	Network     string          `json:"network"`
	TLS         int             `json:"tls"`
	Flow        string          `json:"flow"`
	TLSSettings TLSSettings     `json:"tls_settings"`
	BaseConfig  BaseConfig      `json:"base_config"`
	Routes      json.RawMessage `json:"routes"`
}

type TLSSettings struct {
	AllowInsecure bool   `json:"allow_insecure"`
	ServerPort    string `json:"server_port"`
	ServerName    string `json:"server_name"`
	PublicKey     string `json:"public_key"`
	PrivateKey    string `json:"private_key"`
	ShortID       string `json:"short_id"`
	shortIDs      []string
}

func (t *TLSSettings) UnmarshalJSON(data []byte) error {
	var raw struct {
		AllowInsecure bool            `json:"allow_insecure"`
		ServerPort    string          `json:"server_port"`
		ServerName    string          `json:"server_name"`
		PublicKey     string          `json:"public_key"`
		PrivateKey    string          `json:"private_key"`
		ShortID       json.RawMessage `json:"short_id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	t.AllowInsecure = raw.AllowInsecure
	t.ServerPort = raw.ServerPort
	t.ServerName = raw.ServerName
	t.PublicKey = raw.PublicKey
	t.PrivateKey = raw.PrivateKey

	shortIDs, err := decodeShortIDs(raw.ShortID)
	if err != nil {
		return err
	}
	t.shortIDs = shortIDs
	if len(shortIDs) > 0 {
		t.ShortID = shortIDs[0]
	}
	return nil
}

func (t TLSSettings) ShortIDList() []string {
	if len(t.shortIDs) > 0 {
		return cloneStrings(t.shortIDs)
	}
	if strings.TrimSpace(t.ShortID) == "" {
		return nil
	}
	return []string{strings.TrimSpace(t.ShortID)}
}

type BaseConfig struct {
	PushInterval FlexibleInt `json:"push_interval"`
	PullInterval FlexibleInt `json:"pull_interval"`
}

type User struct {
	ID          int    `json:"id"`
	UUID        string `json:"uuid"`
	SpeedLimit  int    `json:"speed_limit"`
	DeviceLimit int    `json:"device_limit"`
}

type TrafficData struct {
	UID      int   `json:"uid"`
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
}

type AliveList map[int][]string

func (a AliveList) Counts() map[int]int {
	counts := make(map[int]int, len(a))
	for uid, ips := range a {
		counts[uid] = len(ips)
	}
	return counts
}

type FlexibleInt int

func (i *FlexibleInt) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*i = 0
		return nil
	}

	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			*i = 0
			return nil
		}
		value, err := strconv.Atoi(text)
		if err != nil {
			return fmt.Errorf("invalid integer string %q: %w", text, err)
		}
		*i = FlexibleInt(value)
		return nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("invalid integer %q: %w", raw, err)
	}
	*i = FlexibleInt(value)
	return nil
}

func NewClient(baseURL, token string, nodeID int) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		nodeID:  nodeID,
		httpClient: &http.Client{
			Timeout: httpTimeout,
		},
	}
}

func (c *Client) GetNodeConfig() (*NodeConfig, error) {
	body, statusCode, headers, err := c.doWithHeaders(http.MethodGet, c.endpoint("/api/v1/server/UniProxy/config"), "", c.configRequestHeaders())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Xboard node config: %w", err)
	}
	if statusCode == http.StatusNotModified {
		return cloneNodeConfig(c.config), nil
	}

	var config NodeConfig
	if err := json.Unmarshal(body, &config); err != nil {
		return nil, fmt.Errorf("failed to decode Xboard node config: %w", err)
	}
	c.configETag = headers.Get("Etag")
	if c.configETag == "" {
		c.configETag = headers.Get("ETag")
	}
	c.config = cloneNodeConfig(&config)
	return cloneNodeConfig(c.config), nil
}

func (c *Client) GetUsers() ([]User, error) {
	body, statusCode, headers, err := c.doWithHeaders(http.MethodGet, c.endpoint("/api/v1/server/UniProxy/user"), "", c.userRequestHeaders())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Xboard users: %w", err)
	}
	if statusCode == http.StatusNotModified {
		return cloneUsers(c.users), nil
	}

	var resp struct {
		Users []User `json:"users"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		var alt []User
		if err := json.Unmarshal(body, &alt); err == nil {
			resp.Users = alt
		} else {
			return nil, fmt.Errorf("failed to decode Xboard users: %w", err)
		}
	}
	c.userETag = headers.Get("Etag")
	if c.userETag == "" {
		c.userETag = headers.Get("ETag")
	}
	c.users = cloneUsers(resp.Users)
	return cloneUsers(resp.Users), nil
}

func (c *Client) GetUserAlive() (AliveList, error) {
	body, err := c.do(http.MethodGet, c.endpoint("/api/v1/server/UniProxy/alivelist"), "")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Xboard alive list: %w", err)
	}

	return decodeAliveList(body)
}

func (c *Client) PushTraffic(data []TrafficData) error {
	trafficMap := make(map[string][2]int64, len(data))
	for _, d := range data {
		key := fmt.Sprintf("%d", d.UID)
		trafficMap[key] = [2]int64{d.Upload, d.Download}
	}

	payload, err := json.Marshal(trafficMap)
	if err != nil {
		return fmt.Errorf("failed to encode traffic payload: %w", err)
	}

	if _, err := c.do(http.MethodPost, c.endpoint("/api/v1/server/UniProxy/push"), string(payload)); err != nil {
		return fmt.Errorf("failed to push traffic to Xboard: %w", err)
	}
	return nil
}

func (c *Client) SendAlive(onlineUsers map[int][]string) error {
	if onlineUsers == nil {
		onlineUsers = map[int][]string{}
	}

	payload := make(map[string][]string, len(onlineUsers))
	for uid, ips := range onlineUsers {
		payload[fmt.Sprintf("%d", uid)] = ips
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode alive payload: %w", err)
	}

	if _, err := c.do(http.MethodPost, c.endpoint("/api/v1/server/UniProxy/alive"), string(jsonBytes)); err != nil {
		return fmt.Errorf("failed to report online users to Xboard: %w", err)
	}
	return nil
}

func (c *Client) do(method, endpoint, jsonBody string) ([]byte, error) {
	body, _, _, err := c.doWithHeaders(method, endpoint, jsonBody, nil)
	return body, err
}

func (c *Client) doWithHeaders(method, endpoint, jsonBody string, headers http.Header) ([]byte, int, http.Header, error) {
	var lastErr error

	for attempt := 1; attempt <= requestMaxAttempts; attempt++ {
		body, statusCode, respHeaders, err := c.doOnce(method, endpoint, jsonBody, headers)
		if err == nil {
			return body, statusCode, respHeaders, nil
		}

		lastErr = err
		if attempt == requestMaxAttempts || !shouldRetry(statusCode) {
			return nil, statusCode, respHeaders, lastErr
		}

		time.Sleep(backoffDelay(attempt))
	}

	return nil, 0, nil, lastErr
}

func (c *Client) doOnce(method, endpoint, jsonBody string, headers http.Header) ([]byte, int, http.Header, error) {
	var requestBody io.Reader
	if jsonBody != "" {
		requestBody = strings.NewReader(jsonBody)
	}

	req, err := http.NewRequest(method, endpoint, requestBody)
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, resp.Header, err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotModified {
		return nil, resp.StatusCode, resp.Header, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, resp.StatusCode, resp.Header, nil
}

func (c *Client) endpoint(path string) string {
	query := url.Values{}
	query.Set("node_id", fmt.Sprintf("%d", c.nodeID))
	query.Set("node_type", "vless")
	query.Set("token", c.token)
	return fmt.Sprintf("%s%s?%s", c.baseURL, path, query.Encode())
}

func (c *Client) userRequestHeaders() http.Header {
	headers := http.Header{}
	if c.userETag != "" {
		headers.Set("If-None-Match", c.userETag)
	}
	return headers
}

func (c *Client) configRequestHeaders() http.Header {
	headers := http.Header{}
	if c.configETag != "" {
		headers.Set("If-None-Match", c.configETag)
	}
	return headers
}

func shouldRetry(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

func backoffDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	return requestRetryBaseWait * time.Duration(1<<(attempt-1))
}

func decodeAliveList(body []byte) (AliveList, error) {
	if len(body) == 0 {
		return AliveList{}, nil
	}

	if alive, ok := tryDecodeAliveIPs(body); ok {
		return alive, nil
	}
	if alive, ok := tryDecodeAliveCounts(body); ok {
		return alive, nil
	}

	return nil, fmt.Errorf("failed to decode Xboard alive list")
}

func decodeShortIDs(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" || strings.TrimSpace(string(raw)) == "null" {
		return nil, nil
	}

	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return splitShortIDs(single), nil
	}

	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return cleanShortIDs(list), nil
	}

	return nil, fmt.Errorf("invalid tls_settings.short_id")
}

func splitShortIDs(value string) []string {
	if strings.Contains(value, ",") {
		return cleanShortIDs(strings.Split(value, ","))
	}
	return cleanShortIDs([]string{value})
}

func cleanShortIDs(values []string) []string {
	output := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			output = append(output, value)
		}
	}
	return output
}

func tryDecodeAliveIPs(body []byte) (AliveList, bool) {
	var direct map[string][]string
	if err := json.Unmarshal(body, &direct); err == nil {
		return convertStringIPsMap(direct), true
	}

	var wrapped struct {
		Alive map[string][]string `json:"alive"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Alive != nil {
		return convertStringIPsMap(wrapped.Alive), true
	}

	return nil, false
}

func tryDecodeAliveCounts(body []byte) (AliveList, bool) {
	if len(body) == 0 {
		return nil, false
	}

	var direct map[string]int
	if err := json.Unmarshal(body, &direct); err == nil {
		return convertStringCountMap(direct), true
	}

	var list []struct {
		UID   int `json:"uid"`
		Alive int `json:"alive"`
	}
	if err := json.Unmarshal(body, &list); err == nil {
		alive := make(AliveList, len(list))
		for _, item := range list {
			alive[item.UID] = placeholderAliveIPs(item.UID, item.Alive)
		}
		return alive, true
	}

	var wrapped struct {
		Alive map[string]int `json:"alive"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Alive != nil {
		return convertStringCountMap(wrapped.Alive), true
	}

	return nil, false
}

func convertStringIPsMap(input map[string][]string) AliveList {
	output := make(AliveList, len(input))
	for key, value := range input {
		var uid int
		if _, err := fmt.Sscanf(key, "%d", &uid); err == nil {
			output[uid] = cloneStrings(value)
		}
	}
	return output
}

func convertStringCountMap(input map[string]int) AliveList {
	output := make(AliveList, len(input))
	for key, value := range input {
		var uid int
		if _, err := fmt.Sscanf(key, "%d", &uid); err == nil {
			output[uid] = placeholderAliveIPs(uid, value)
		}
	}
	return output
}

func placeholderAliveIPs(uid, count int) []string {
	if count <= 0 {
		return nil
	}
	ips := make([]string, count)
	for i := range ips {
		ips[i] = fmt.Sprintf("remote-%d-%d", uid, i)
	}
	return ips
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func cloneUsers(users []User) []User {
	if users == nil {
		return nil
	}
	cloned := make([]User, len(users))
	copy(cloned, users)
	return cloned
}

func cloneNodeConfig(config *NodeConfig) *NodeConfig {
	if config == nil {
		return nil
	}

	cloned := *config
	if config.Routes != nil {
		cloned.Routes = append(json.RawMessage(nil), config.Routes...)
	}
	return &cloned
}
