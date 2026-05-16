package cloudflare

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DNSManager 负责创建、更新和删除单个 Cloudflare A 记录。
type DNSManager struct {
	apiToken   string
	zoneID     string
	recordName string
	recordID   string
	baseURL    string
	httpClient *http.Client
}

type dnsRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

type apiError struct {
	Message string `json:"message"`
}

type apiEnvelope[T any] struct {
	Success bool       `json:"success"`
	Result  T          `json:"result"`
	Errors  []apiError `json:"errors"`
}

func NewDNSManager(apiToken, zoneID, recordName string) *DNSManager {
	return &DNSManager{
		apiToken:   apiToken,
		zoneID:     zoneID,
		recordName: recordName,
		baseURL:    "https://api.cloudflare.com/client/v4",
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (d *DNSManager) Register(publicIP string) error {
	normalizedIP, err := normalizePublicIP(publicIP)
	if err != nil {
		return err
	}

	record, err := d.findRecord()
	if err != nil {
		return err
	}

	if record != nil {
		d.recordID = record.ID
		if record.Content == normalizedIP {
			return nil
		}

		updatedRecord, err := d.upsertRecord(http.MethodPut, record.ID, normalizedIP)
		if err != nil {
			return err
		}
		d.recordID = updatedRecord.ID
		return nil
	}

	createdRecord, err := d.upsertRecord(http.MethodPost, "", normalizedIP)
	if err != nil {
		return err
	}
	d.recordID = createdRecord.ID
	return nil
}

func (d *DNSManager) Deregister() error {
	if d.recordID == "" {
		record, err := d.findRecord()
		if err != nil {
			return err
		}
		if record == nil {
			return nil
		}
		d.recordID = record.ID
	}

	endpoint := fmt.Sprintf("%s/%s", d.recordsEndpoint(), url.PathEscape(d.recordID))

	var lastErr error
	for i := 0; i < 3; i++ {
		body, err := d.doRequest(http.MethodDelete, endpoint, nil)
		if err == nil {
			if _, decodeErr := decodeResponse[struct{}](body); decodeErr == nil {
				d.recordID = ""
				return nil
			} else {
				lastErr = decodeErr
			}
		} else {
			lastErr = err
		}

		time.Sleep(time.Second * time.Duration(i+1))
	}

	return fmt.Errorf("删除 Cloudflare DNS 记录失败，已重试 3 次: %w", lastErr)
}

func (d *DNSManager) findRecord() (*dnsRecord, error) {
	query := url.Values{}
	query.Set("type", "A")
	query.Set("name", d.recordName)

	body, err := d.doRequest(http.MethodGet, d.recordsEndpoint()+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}

	records, err := decodeResponse[[]dnsRecord](body)
	if err != nil {
		return nil, err
	}

	if len(records) == 0 {
		return nil, nil
	}
	return &records[0], nil
}

func (d *DNSManager) upsertRecord(method, recordID, publicIP string) (*dnsRecord, error) {
	payload := map[string]interface{}{
		"type":    "A",
		"name":    d.recordName,
		"content": publicIP,
		"ttl":     60,
		"proxied": false,
		"comment": "phantom-node 自动注册",
	}

	endpoint := d.recordsEndpoint()
	if method == http.MethodPut {
		endpoint = fmt.Sprintf("%s/%s", endpoint, url.PathEscape(recordID))
	}

	body, err := d.doRequest(method, endpoint, payload)
	if err != nil {
		return nil, err
	}

	record, err := decodeResponse[dnsRecord](body)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (d *DNSManager) doRequest(method, endpoint string, payload interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("序列化 Cloudflare 请求失败: %w", err)
		}
		bodyReader = bytes.NewReader(payloadBytes)
	}

	req, err := http.NewRequest(method, endpoint, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 Cloudflare API 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Cloudflare API 返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return respBody, nil
}

func (d *DNSManager) recordsEndpoint() string {
	return fmt.Sprintf("%s/zones/%s/dns_records", strings.TrimRight(d.baseURL, "/"), url.PathEscape(d.zoneID))
}

func decodeResponse[T any](body []byte) (T, error) {
	var zero T
	var envelope apiEnvelope[T]
	if err := json.Unmarshal(body, &envelope); err != nil {
		return zero, fmt.Errorf("解析 Cloudflare 响应失败: %w", err)
	}

	if !envelope.Success {
		errMsg := "未知错误"
		if len(envelope.Errors) > 0 && envelope.Errors[0].Message != "" {
			errMsg = envelope.Errors[0].Message
		}
		return zero, fmt.Errorf("Cloudflare API 返回错误: %s", errMsg)
	}

	return envelope.Result, nil
}

func normalizePublicIP(raw string) (string, error) {
	ip := strings.TrimSpace(raw)
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		return "", fmt.Errorf("公网 IPv4 地址不合法: %q", raw)
	}
	return ip, nil
}

func GetPublicIP() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	endpoints := []string{
		"https://api.ipify.org",
		"https://ip.sb",
		"https://ifconfig.me/ip",
	}

	for _, endpoint := range endpoints {
		resp, err := client.Get(endpoint)
		if err != nil {
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK {
			continue
		}

		ip, normalizeErr := normalizePublicIP(string(body))
		if normalizeErr == nil {
			return ip, nil
		}
	}

	return "", fmt.Errorf("从所有公网 IP 查询源获取 IPv4 失败")
}
