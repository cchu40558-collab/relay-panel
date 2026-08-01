package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/util/netsafe"
)

const (
	realityScanTimeout     = 10 * time.Second
	realityDiscoverTimeout = 4 * time.Second
	realityScanConcurrency = 32
	realityDiscoverMaxIPs  = 256
	realityScanMaxTotal    = 512

	// The default pool is intentionally tested more than once. A target that
	// happens to complete one ordinary TLS handshake is not a stable REALITY
	// target; the old deployment script used the same ten-round policy.
	realityStabilityRounds       = 10
	realityStabilityTimeout      = 3 * time.Second
	realityStabilityMinSuccesses = 8
	realityStabilityMaxMedianMs  = 1500
	realityStabilityConcurrency  = 16
)

var defaultRealityScanCandidates = []string{
	"www.cloudflare.com:443",
	"developers.cloudflare.com:443",
	"cdnjs.cloudflare.com:443",
	"blog.cloudflare.com:443",
	"dash.cloudflare.com:443",
	"www.cloudflarestatus.com:443",
	"one.one.one.one:443",
	"cloudflare-dns.com:443",
	"pages.dev:443",
	"workers.dev:443",
	"www.apple.com:443",
	"support.apple.com:443",
	"developer.apple.com:443",
	"www.itunes.com:443",
	"www.icloud.com:443",
	"music.apple.com:443",
	"tv.apple.com:443",
	"apps.apple.com:443",
	"beta.apple.com:443",
	"appleid.apple.com:443",
	"www.amazon.com:443",
	"aws.amazon.com:443",
	"docs.aws.amazon.com:443",
	"www.amazon.co.uk:443",
	"www.amazon.de:443",
	"www.amazon.co.jp:443",
	"www.amazon.ca:443",
	"m.media-amazon.com:443",
	"images-na.ssl-images-amazon.com:443",
	"www.google.com:443",
	"www.gstatic.com:443",
	"fonts.gstatic.com:443",
	"www.youtube.com:443",
	"www.android.com:443",
	"dl.google.com:443",
	"accounts.google.com:443",
	"maps.google.com:443",
	"play.google.com:443",
	"www.googleapis.com:443",
	"github.com:443",
	"docs.github.com:443",
	"assets.github.com:443",
	"api.github.com:443",
	"github.githubassets.com:443",
	"raw.githubusercontent.com:443",
	"www.samsung.com:443",
	"www.nvidia.com:443",
	"www.intel.com:443",
	"www.amd.com:443",
	"www.sony.com:443",
	"www.lg.com:443",
	"www.lenovo.com:443",
	"www.dell.com:443",
	"www.hp.com:443",
	"www.asus.com:443",
	"www.paypal.com:443",
	"www.linkedin.com:443",
	"www.dropbox.com:443",
	"www.spotify.com:443",
	"www.netflix.com:443",
	"www.salesforce.com:443",
	"www.ibm.com:443",
	"www.oracle.com:443",
	"www.mozilla.org:443",
	"www.wikipedia.org:443",
	"www.reddit.com:443",
	"www.twitch.tv:443",
	"www.shopify.com:443",
	"www.ebay.com:443",
	"slack.com:443",
	"discord.com:443",
	"zoom.us:443",
	"www.notion.so:443",
	"www.canva.com:443",
	"wordpress.com:443",
	"www.fastly.com:443",
	"www.akamai.com:443",
	"www.digitalocean.com:443",
	"www.heroku.com:443",
	"www.vercel.com:443",
	"www.netlify.com:443",
	"www.figma.com:443",
	"www.atlassian.com:443",
	"about.gitlab.com:443",
	"bitbucket.org:443",
	"www.facebook.com:443",
	"www.instagram.com:443",
	"www.whatsapp.com:443",
	"www.tiktok.com:443",
	"www.pinterest.com:443",
	"www.imdb.com:443",
	"www.bbc.com:443",
	"www.nytimes.com:443",
	"www.cnn.com:443",
	"weather.com:443",
	"www.booking.com:443",
	"www.airbnb.com:443",
	"www.uber.com:443",
	"soundcloud.com:443",
	"www.roblox.com:443",
}

type RealityScanResult struct {
	Target      string   `json:"target" example:"www.cloudflare.com:443"`
	Host        string   `json:"host" example:"www.cloudflare.com"`
	IP          string   `json:"ip" example:"104.16.124.96"`
	Port        int      `json:"port" example:"443"`
	Feasible    bool     `json:"feasible" example:"true"`
	TLS13       bool     `json:"tls13" example:"true"`
	TLSVersion  string   `json:"tlsVersion" example:"1.3"`
	H2          bool     `json:"h2" example:"true"`
	ALPN        string   `json:"alpn" example:"h2"`
	X25519      bool     `json:"x25519" example:"true"`
	CurveID     string   `json:"curveID" example:"X25519"`
	CertValid   bool     `json:"certValid" example:"true"`
	CertSubject string   `json:"certSubject" example:"cloudflare.com"`
	CertIssuer  string   `json:"certIssuer" example:"Google Trust Services"`
	NotAfter    string   `json:"notAfter" example:"2026-08-01T00:00:00Z"`
	ServerNames []string `json:"serverNames"`
	LatencyMs   int      `json:"latencyMs" example:"180"`
	Rounds      int      `json:"rounds" example:"10"`
	Successes   int      `json:"successes" example:"10"`
	MedianMs    int      `json:"medianMs" example:"180"`
	AverageMs   int      `json:"averageMs" example:"184"`
	BestMs      int      `json:"bestMs" example:"162"`
	WorstMs     int      `json:"worstMs" example:"219"`
	JitterMs    int      `json:"jitterMs" example:"57"`
	Reason      string   `json:"reason" example:""`
}

type realityProbeTask struct {
	dialHost string
	port     int
	sni      string
	timeout  time.Duration
	bulk     bool
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "1.3"
	case tls.VersionTLS12:
		return "1.2"
	case tls.VersionTLS11:
		return "1.1"
	case tls.VersionTLS10:
		return "1.0"
	default:
		return "unknown"
	}
}

func realityCurveName(id tls.CurveID) string {
	switch id {
	case tls.X25519:
		return "X25519"
	case tls.X25519MLKEM768:
		return "X25519MLKEM768"
	case tls.CurveP256:
		return "P-256"
	case tls.CurveP384:
		return "P-384"
	case tls.CurveP521:
		return "P-521"
	case 0:
		return ""
	default:
		return fmt.Sprintf("0x%04x", uint16(id))
	}
}

func filterUsableSANs(dnsNames []string) []string {
	out := make([]string, 0, len(dnsNames))
	for _, n := range dnsNames {
		n = strings.TrimSpace(n)
		if n == "" || strings.HasPrefix(n, "*.") {
			continue
		}
		out = append(out, n)
	}
	return out
}

func firstUsableName(leaf *x509.Certificate) string {
	cn := strings.TrimSpace(leaf.Subject.CommonName)
	if cn != "" && !strings.HasPrefix(cn, "*.") {
		return cn
	}
	for _, n := range leaf.DNSNames {
		n = strings.TrimSpace(n)
		if n != "" && !strings.HasPrefix(n, "*.") {
			return n
		}
	}
	return ""
}

func splitRealityTarget(target string) (string, int, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", 0, common.NewError("target is required")
	}
	host, portStr := target, "443"
	if h, p, err := net.SplitHostPort(target); err == nil {
		host, portStr = h, p
	}
	host, err := netsafe.NormalizeHost(host)
	if err != nil {
		return "", 0, common.NewError("invalid target host: ", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, common.NewError("invalid target port")
	}
	return host, port, nil
}

func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func enumerateCIDR(cidr string, max int) ([]string, error) {
	_, ipnet, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return nil, err
	}
	ips := make([]string, 0, max)
	for ip := ipnet.IP.Mask(ipnet.Mask); ipnet.Contains(ip); incIP(ip) {
		ips = append(ips, ip.String())
		if len(ips) >= max {
			break
		}
	}
	return ips, nil
}

func (s *ServerService) probeRealityAddr(dialHost string, port int, sni string, timeout time.Duration, xver int) *RealityScanResult {
	addr := net.JoinHostPort(dialHost, strconv.Itoa(port))
	res := &RealityScanResult{Port: port}
	if net.ParseIP(dialHost) != nil {
		res.IP = dialHost
	}
	if sni != "" {
		res.Host = sni
		res.Target = net.JoinHostPort(sni, strconv.Itoa(port))
	} else {
		res.Host = dialHost
		res.Target = addr
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	conn, err := netsafe.SSRFGuardedDialContext(ctx, "tcp", addr)
	if err != nil {
		res.Reason = "connection failed: " + err.Error()
		return res
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	// A REALITY inbound with xver>=1 fronts a target that speaks the PROXY
	// protocol (e.g. an Nginx listener with `proxy_protocol`), so the probe
	// must lead with a PROXY header or the target resets the connection and
	// the scan reports a spurious handshake failure (#6082).
	if xver >= 1 {
		if err := writeProxyProtocolHeader(conn, xver); err != nil {
			res.Reason = "proxy protocol write failed: " + err.Error()
			return res
		}
	}

	cfg := &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2", "http/1.1"},
		CurvePreferences:   []tls.CurveID{tls.X25519, tls.X25519MLKEM768},
		MinVersion:         tls.VersionTLS12,
	}
	tlsConn := tls.Client(conn, cfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		res.Reason = "TLS handshake failed: " + err.Error()
		return res
	}
	res.LatencyMs = int(time.Since(start).Milliseconds())

	st := tlsConn.ConnectionState()
	res.TLS13 = st.Version == tls.VersionTLS13
	res.TLSVersion = tlsVersionName(st.Version)
	res.ALPN = st.NegotiatedProtocol
	res.H2 = st.NegotiatedProtocol == "h2"
	res.CurveID = realityCurveName(st.CurveID)
	res.X25519 = st.CurveID == tls.X25519 || st.CurveID == tls.X25519MLKEM768

	verifyHost := sni
	if len(st.PeerCertificates) > 0 {
		leaf := st.PeerCertificates[0]
		res.CertSubject = leaf.Subject.CommonName
		if res.CertSubject == "" && len(leaf.DNSNames) > 0 {
			res.CertSubject = leaf.DNSNames[0]
		}
		if len(leaf.Issuer.Organization) > 0 {
			res.CertIssuer = leaf.Issuer.Organization[0]
		} else {
			res.CertIssuer = leaf.Issuer.CommonName
		}
		res.NotAfter = leaf.NotAfter.UTC().Format(time.RFC3339)
		res.ServerNames = filterUsableSANs(leaf.DNSNames)

		if sni == "" {
			if discovered := firstUsableName(leaf); discovered != "" {
				res.Host = discovered
				res.Target = net.JoinHostPort(discovered, strconv.Itoa(port))
				verifyHost = discovered
			}
		}

		if verifyHost != "" {
			opts := x509.VerifyOptions{DNSName: verifyHost, Intermediates: x509.NewCertPool()}
			for _, c := range st.PeerCertificates[1:] {
				opts.Intermediates.AddCert(c)
			}
			if _, verr := leaf.Verify(opts); verr == nil {
				res.CertValid = true
			} else {
				res.Reason = "certificate not trusted: " + verr.Error()
			}
		} else {
			res.Reason = "no usable domain in certificate"
		}
	} else {
		res.Reason = "no certificate presented"
	}

	res.Feasible = res.TLS13 && res.H2 && res.X25519 && res.CertValid
	if !res.Feasible && res.Reason == "" {
		switch {
		case !res.TLS13:
			res.Reason = "server does not negotiate TLS 1.3"
		case !res.H2:
			res.Reason = "server does not negotiate HTTP/2 (h2)"
		case !res.X25519:
			res.Reason = "server did not use X25519 key exchange"
		}
	}
	return res
}

func (s *ServerService) probeRealityTarget(host string, port int, xver int) *RealityScanResult {
	return s.probeRealityAddr(host, port, host, realityScanTimeout, xver)
}

func (s *ServerService) ScanRealityTarget(target string, xver int) (*RealityScanResult, error) {
	host, port, err := splitRealityTarget(target)
	if err != nil {
		return nil, err
	}
	result := s.probeRealityTarget(host, port, xver)
	setSingleRealityMeasurement(result)
	return result, nil
}

func (s *ServerService) ScanRealityTargets(targetsCSV string) ([]*RealityScanResult, error) {
	var tokens []string
	for raw := range strings.SplitSeq(targetsCSV, ",") {
		if t := strings.TrimSpace(raw); t != "" {
			tokens = append(tokens, t)
		}
	}
	usingDefaultPool := len(tokens) == 0
	if usingDefaultPool {
		tokens = append(tokens, defaultRealityScanCandidates...)
	}

	var tasks []realityProbeTask
	var invalid []*RealityScanResult
	for _, token := range tokens {
		if len(tasks) >= realityScanMaxTotal {
			break
		}
		if strings.Contains(token, "/") {
			ips, err := enumerateCIDR(token, realityDiscoverMaxIPs)
			if err != nil {
				invalid = append(invalid, &RealityScanResult{Target: token, Reason: "invalid CIDR: " + err.Error()})
				continue
			}
			for _, ip := range ips {
				if len(tasks) >= realityScanMaxTotal {
					break
				}
				tasks = append(tasks, realityProbeTask{dialHost: ip, port: 443, timeout: realityDiscoverTimeout, bulk: true})
			}
			continue
		}
		host, port, err := splitRealityTarget(token)
		if err != nil {
			invalid = append(invalid, &RealityScanResult{Target: token, Reason: err.Error()})
			continue
		}
		if net.ParseIP(host) != nil {
			tasks = append(tasks, realityProbeTask{dialHost: host, port: port, timeout: realityDiscoverTimeout})
		} else {
			tasks = append(tasks, realityProbeTask{dialHost: host, port: port, sni: host, timeout: realityScanTimeout})
		}
	}

	probed := make([]*RealityScanResult, len(tasks))
	concurrency := realityScanConcurrency
	if usingDefaultPool {
		concurrency = realityStabilityConcurrency
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, task := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, tk realityProbeTask) {
			defer wg.Done()
			defer func() { <-sem }()
			var r *RealityScanResult
			if usingDefaultPool && tk.sni != "" {
				r = s.probeRealityTargetStability(tk.dialHost, tk.port, tk.sni)
			} else {
				r = s.probeRealityAddr(tk.dialHost, tk.port, tk.sni, tk.timeout, 0)
				setSingleRealityMeasurement(r)
			}
			if tk.bulk && r.TLSVersion == "" {
				return
			}
			probed[idx] = r
		}(i, task)
	}
	wg.Wait()

	results := dedupRealityResults(append(probed, invalid...))
	sortRealityResults(results)
	return results, nil
}

func (s *ServerService) probeRealityTargetStability(dialHost string, port int, sni string) *RealityScanResult {
	var best *RealityScanResult
	latencies := make([]int, 0, realityStabilityRounds)
	failures := make(map[string]int)

	for range realityStabilityRounds {
		result := s.probeRealityAddr(dialHost, port, sni, realityStabilityTimeout, 0)
		if result.Feasible {
			latencies = append(latencies, result.LatencyMs)
			if best == nil || result.LatencyMs < best.LatencyMs {
				best = result
			}
			continue
		}
		if result.Reason != "" {
			failures[result.Reason]++
		}
		if best == nil {
			best = result
		}
	}
	if best == nil {
		best = &RealityScanResult{Target: net.JoinHostPort(sni, strconv.Itoa(port)), Host: sni, Port: port}
	}

	best.Rounds = realityStabilityRounds
	best.Successes = len(latencies)
	if len(latencies) > 0 {
		slices.Sort(latencies)
		best.BestMs = latencies[0]
		best.WorstMs = latencies[len(latencies)-1]
		best.JitterMs = best.WorstMs - best.BestMs
		best.MedianMs = medianLatency(latencies)
		best.AverageMs = averageLatency(latencies)
		best.LatencyMs = best.MedianMs
	}
	best.Feasible = best.Successes >= realityStabilityMinSuccesses && best.MedianMs > 0 && best.MedianMs <= realityStabilityMaxMedianMs
	if best.Feasible {
		best.Reason = ""
	} else if best.Successes < realityStabilityMinSuccesses {
		best.Reason = fmt.Sprintf("stable TLS requirements not met: %d/%d feasible probes", best.Successes, best.Rounds)
	} else if best.MedianMs > realityStabilityMaxMedianMs {
		best.Reason = fmt.Sprintf("median latency %dms exceeds %dms", best.MedianMs, realityStabilityMaxMedianMs)
	} else {
		best.Reason = mostCommonRealityFailure(failures)
	}
	return best
}

func setSingleRealityMeasurement(result *RealityScanResult) {
	if result == nil {
		return
	}
	result.Rounds = 1
	if result.Feasible {
		result.Successes = 1
		result.MedianMs = result.LatencyMs
		result.AverageMs = result.LatencyMs
		result.BestMs = result.LatencyMs
		result.WorstMs = result.LatencyMs
	}
}

func medianLatency(latencies []int) int {
	if len(latencies)%2 == 1 {
		return latencies[len(latencies)/2]
	}
	return (latencies[len(latencies)/2-1] + latencies[len(latencies)/2]) / 2
}

func averageLatency(latencies []int) int {
	total := 0
	for _, latency := range latencies {
		total += latency
	}
	return total / len(latencies)
}

func mostCommonRealityFailure(failures map[string]int) string {
	message := "no feasible TLS probes"
	count := 0
	for reason, n := range failures {
		if n > count || (n == count && reason < message) {
			message, count = reason, n
		}
	}
	return message
}

func dedupRealityResults(results []*RealityScanResult) []*RealityScanResult {
	best := make(map[string]*RealityScanResult)
	order := make([]string, 0, len(results))
	for _, r := range results {
		if r == nil {
			continue
		}
		if ex, ok := best[r.Target]; !ok {
			best[r.Target] = r
			order = append(order, r.Target)
		} else if betterRealityResult(r, ex) {
			best[r.Target] = r
		}
	}
	out := make([]*RealityScanResult, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	return out
}

func betterRealityResult(a, b *RealityScanResult) bool {
	if a.Feasible != b.Feasible {
		return a.Feasible
	}
	if a.Successes != b.Successes {
		return a.Successes > b.Successes
	}
	if a.MedianMs != b.MedianMs {
		return a.MedianMs > 0 && (b.MedianMs == 0 || a.MedianMs < b.MedianMs)
	}
	return a.LatencyMs > 0 && (b.LatencyMs == 0 || a.LatencyMs < b.LatencyMs)
}

func sortRealityResults(results []*RealityScanResult) {
	slices.SortStableFunc(results, func(a, b *RealityScanResult) int {
		if a.Feasible != b.Feasible {
			if a.Feasible {
				return -1
			}
			return 1
		}
		if a.Successes != b.Successes {
			return b.Successes - a.Successes
		}
		if a.MedianMs != b.MedianMs {
			return a.MedianMs - b.MedianMs
		}
		if a.AverageMs != b.AverageMs {
			return a.AverageMs - b.AverageMs
		}
		if a.JitterMs != b.JitterMs {
			return a.JitterMs - b.JitterMs
		}
		return strings.Compare(a.Target, b.Target)
	})
}

// writeProxyProtocolHeader emits a PROXY protocol header describing the local
// connection so a target that requires it (Nginx `proxy_protocol`, matching a
// REALITY inbound's xver) accepts the probe instead of resetting it. xver 1
// sends the human-readable v1 header; xver 2 sends the binary v2 header. The
// addresses come from the already-dialed connection, so they are always a
// consistent, real (src, dst) pair.
func writeProxyProtocolHeader(conn net.Conn, xver int) error {
	local, lok := conn.LocalAddr().(*net.TCPAddr)
	remote, rok := conn.RemoteAddr().(*net.TCPAddr)
	if !lok || !rok {
		return fmt.Errorf("connection has no TCP addresses")
	}
	if xver >= 2 {
		return writeProxyProtocolV2(conn, local, remote)
	}
	return writeProxyProtocolV1(conn, local, remote)
}

func writeProxyProtocolV1(conn net.Conn, local, remote *net.TCPAddr) error {
	fam := "TCP4"
	if local.IP.To4() == nil || remote.IP.To4() == nil {
		fam = "TCP6"
	}
	header := fmt.Sprintf("PROXY %s %s %s %d %d\r\n", fam, local.IP.String(), remote.IP.String(), local.Port, remote.Port)
	_, err := conn.Write([]byte(header))
	return err
}

func writeProxyProtocolV2(conn net.Conn, local, remote *net.TCPAddr) error {
	buf := []byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A}
	buf = append(buf, 0x21)

	src4, dst4 := local.IP.To4(), remote.IP.To4()
	if src4 != nil && dst4 != nil {
		buf = append(buf, 0x11)
		buf = append(buf, 0x00, 12)
		buf = append(buf, src4...)
		buf = append(buf, dst4...)
		buf = append(buf, byte(local.Port>>8), byte(local.Port))
		buf = append(buf, byte(remote.Port>>8), byte(remote.Port))
	} else {
		buf = append(buf, 0x21)
		buf = append(buf, 0x00, 36)
		buf = append(buf, local.IP.To16()...)
		buf = append(buf, remote.IP.To16()...)
		buf = append(buf, byte(local.Port>>8), byte(local.Port))
		buf = append(buf, byte(remote.Port>>8), byte(remote.Port))
	}
	_, err := conn.Write(buf)
	return err
}
