package panel

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is an HTTP client for the Xboard UniProxy node APIs.
type Client struct {
	baseURL    string
	token      string
	nodeID     int
	httpClient *http.Client
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
}

type BaseConfig struct {
	PushInterval int `json:"push_interval"`
	PullInterval int `json:"pull_interval"`
}

type User struct {
	ID         int    `json:"id"`
	UUID       string `json:"uuid"`
	SpeedLimit int    `json:"speed_limit"`
}

type TrafficData struct {
	UID      int   `json:"uid"`
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
}

func NewClient(baseURL, token string, nodeID int) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		nodeID:  nodeID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) GetNodeConfig() (*NodeConfig, error) {
	body, err := c.doGet(c.endpoint("/api/v1/server/UniProxy/config"))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch node config: %w", err)
	}

	var config NodeConfig
	if err := json.Unmarshal(body, &config); err != nil {
		return nil, fmt.Errorf("failed to decode node config response: %w", err)
	}
	return &config, nil
}

func (c *Client) GetUsers() ([]User, error) {
	body, err := c.doGet(c.endpoint("/api/v1/server/UniProxy/user"))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user list: %w", err)
	}

	var resp struct {
		Users []User `json:"users"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode user list response: %w", err)
	}
	return resp.Users, nil
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

	_, err = c.doPost(c.endpoint("/api/v1/server/UniProxy/push"), string(payload))
	if err != nil {
		return fmt.Errorf("failed to push traffic: %w", err)
	}
	return nil
}

func (c *Client) SendAlive(onlineUsers []map[string]interface{}) error {
	payload := map[string]interface{}{
		"online_count": onlineUsers,
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode heartbeat payload: %w", err)
	}

	_, err = c.doPost(c.endpoint("/api/v1/server/UniProxy/alive"), string(jsonBytes))
	if err != nil {
		return fmt.Errorf("failed to send heartbeat: %w", err)
	}
	return nil
}

func (c *Client) doGet(endpoint string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (c *Client) doPost(endpoint string, jsonBody string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (c *Client) endpoint(path string) string {
	query := url.Values{}
	query.Set("node_id", fmt.Sprintf("%d", c.nodeID))
	query.Set("token", c.token)
	return fmt.Sprintf("%s%s?%s", c.baseURL, path, query.Encode())
}
