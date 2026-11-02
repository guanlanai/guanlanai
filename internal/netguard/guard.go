package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrInsecureScheme  = errors.New("base_url 必须使用 https")
	ErrUserInfo        = errors.New("base_url 不能包含用户名或密码")
	ErrQueryOrFragment = errors.New("base_url 不能包含 query 或 fragment")
	ErrPathRequired    = errors.New("base_url 必须包含路径，例如 /v1")
	ErrPrivateAddress  = errors.New("base_url 指向私网或保留地址")
	ErrHostNotAllowed  = errors.New("base_url 域名不在白名单")
	ErrRedirect        = errors.New("不允许 HTTP 重定向")
)

var deniedCIDRs = mustCIDRs(
	"100.64.0.0/10", // CGNAT
	"192.0.0.0/24",  // IETF protocol assignments
	"198.18.0.0/15", // benchmarking
	"240.0.0.0/4",   // reserved
	"0.0.0.0/8",     // this network
)

// Policy 控制自定义 OpenAI 兼容地址的访问边界。
type Policy struct {
	AllowHTTP    bool
	AllowPrivate bool
	AllowedHosts []string
}

// Validate 解析并校验 base_url，返回规范化后的地址。
func (p Policy) Validate(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("解析 base_url: %w", err)
	}
	if u.Scheme != "https" && !(p.AllowHTTP && u.Scheme == "http") {
		return "", ErrInsecureScheme
	}
	if u.User != nil {
		return "", ErrUserInfo
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", ErrQueryOrFragment
	}
	if u.Host == "" {
		return "", errors.New("base_url 缺少域名")
	}
	if u.Path == "" || u.Path == "/" {
		return "", ErrPathRequired
	}

	host := strings.ToLower(u.Hostname())
	if !p.allowHost(host) {
		return "", ErrHostNotAllowed
	}
	if !p.AllowPrivate {
		if err := checkPublicHost(ctx, host); err != nil {
			return "", err
		}
	}

	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}

func (p Policy) allowHost(host string) bool {
	if len(p.AllowedHosts) == 0 {
		return true
	}
	for _, allowed := range p.AllowedHosts {
		allowed = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(allowed)), ".")
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func checkPublicHost(ctx context.Context, host string) error {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return ErrPrivateAddress
	}

	if ip := net.ParseIP(host); ip != nil {
		if isDeniedIP(ip) {
			return ErrPrivateAddress
		}
		return nil
	}

	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return fmt.Errorf("解析 base_url 域名: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("base_url 域名没有解析结果")
	}
	for _, addr := range ips {
		if isDeniedIP(addr.IP) {
			return ErrPrivateAddress
		}
	}
	return nil
}

func isDeniedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, cidr := range deniedCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// NewHTTPClient 创建带 SSRF 防护的 HTTP 客户端。
// 它在连接时重新解析并校验 IP，避免 DNS rebinding。
func NewHTTPClient(policy Policy, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("解析连接地址: %w", err)
			}
			host = strings.ToLower(host)
			if !policy.allowHost(host) {
				return nil, ErrHostNotAllowed
			}

			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("解析连接域名: %w", err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("连接域名没有解析结果")
			}
			if !policy.AllowPrivate {
				for _, addr := range ips {
					if isDeniedIP(addr.IP) {
						return nil, ErrPrivateAddress
					}
				}
			}

			var lastErr error
			for _, addr := range ips {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return ErrRedirect
		},
	}
}

func mustCIDRs(values ...string) []*net.IPNet {
	result := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, cidr, err := net.ParseCIDR(value)
		if err != nil {
			panic(err)
		}
		result = append(result, cidr)
	}
	return result
}
