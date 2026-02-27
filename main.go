package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

var (
	targetIPs = flag.String("targets", "127.0.0.1", "target IP")
	portRange = flag.String("port", "1-1024", "port range")
	timeout   = flag.Int("timeout", 2, "timeout in seconds")
	workers   = flag.Int("workers", 100, "number of workers")
	verbose   = flag.Bool("verbose", false, "verbose output")
	protocol  = flag.String("protocol", "tcp", "protocol to use")
	rateLimit = flag.Float64("rate", 0, "max probes per second (0 = no limit)")
)

type Job struct {
	Target   string
	Port     int
	Protocol string
}

type Result struct {
	Target   string
	Port     int
	Protocol string
	Status   string
	Error    string
	Time     time.Duration
}

func main() {
	flag.Parse()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	targets := splitAndTrim(*targetIPs)
	if len(targets) == 0 {
		log.Fatal("No targets provided")
	}
	minPort, maxPort, err := parsePortRange(*portRange)
	if err != nil {
		log.Fatal("Invalid port range: ", err)
	}

	proto := strings.ToLower(strings.TrimSpace(*protocol))
	if proto != "tcp" && proto != "udp" {
		log.Fatal("Invalid protocol: ", proto)
	}

	jobs := make(chan Job, 1024)
	results := make(chan Result, 1024)

	var limiter *rate.Limiter
	if *rateLimit > 0 {
		limiter = rate.NewLimiter(rate.Limit(*rateLimit), *workers)
	} else {
		limiter = rate.NewLimiter(rate.Inf, 0)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// start workers
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go worker(ctx, jobs, results, &wg, *timeout, limiter)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// Enqueue jobs
	go func() {
		for _, target := range targets {
			ip := net.ParseIP(target)
			if ip == nil {
				fmt.Printf("Invalid IP address: %s\n", target)
				continue
			}

			for port := minPort; port <= maxPort; port++ {
				jobs <- Job{
					Target:   target,
					Port:     port,
					Protocol: proto,
				}
			}
		}
		close(jobs)
	}()

	// Collect
	startAll := time.Now()
	openPorts := map[string][]int{}
	stateCounts := map[string]map[string]int{} // host -> state -> count
	total := 0
	headerPrinted := false
	done := false

	for !done {
		select {
		case <-interrupt:
			fmt.Println("\nInterrupted. Exiting (some results may be incomplete).")
			cancel()
			done = true
		case r, ok := <-results:
			if !ok {
				done = true
				break
			}
			total++

			if _, ok := stateCounts[r.Target]; !ok {
				stateCounts[r.Target] = map[string]int{}
			}
			stateCounts[r.Target][r.Status]++

			if !headerPrinted && (r.Status == "open" || *verbose) {
				fmt.Printf("%-14s %-48s %-6s %10s\n", "STATUS", "TARGET:PORT", "PROTO", "RTT")
				fmt.Println(strings.Repeat("-", 82))
				headerPrinted = true
			}

			addr := net.JoinHostPort(r.Target, strconv.Itoa(r.Port))
			if r.Status == "open" {
				openPorts[r.Target] = append(openPorts[r.Target], r.Port)
				fmt.Printf("%-14s %-48s %-6s %10v\n", "open", addr, r.Protocol, r.Time.Round(time.Millisecond))
			} else if *verbose {
				statusStr := strings.ToUpper(r.Status)
				if r.Error != "" {
					fmt.Printf("%-14s %-48s %-6s %10v  %s\n", statusStr, addr, r.Protocol, r.Time.Round(time.Millisecond), r.Error)
				} else {
					fmt.Printf("%-14s %-48s %-6s %10v\n", statusStr, addr, r.Protocol, r.Time.Round(time.Millisecond))
				}
			}
		}
	}

	// Sort open ports per host
	for host := range openPorts {
		sort.Ints(openPorts[host])
	}

	// port scanner summary
	fmt.Println()
	fmt.Println(strings.Repeat("=", 52))
	fmt.Println("  Scan complete")
	fmt.Println(strings.Repeat("=", 52))
	fmt.Printf("  Duration: %v\n", time.Since(startAll).Round(time.Millisecond))
	fmt.Printf("  Total:    %d scanned\n", total)

	openTotal := 0
	hosts := make([]string, 0, len(openPorts))
	for host := range openPorts {
		hosts = append(hosts, host)
		openTotal += len(openPorts[host])
	}
	sort.Strings(hosts)
	fmt.Printf("  Open:     %d port(s)\n", openTotal)
	fmt.Println()

	if len(hosts) > 0 {
		fmt.Println("  Open ports by host:")
		for _, host := range hosts {
			ports := openPorts[host]
			parts := make([]string, 0, len(ports))
			for _, p := range ports {
				if name := portServiceName(p); name != "" {
					parts = append(parts, fmt.Sprintf("%d (%s)", p, name))
				} else {
					parts = append(parts, strconv.Itoa(p))
				}
			}
			fmt.Printf("    %s: %s\n", host, strings.Join(parts, ", "))
		}
		fmt.Println()
	}

	if *verbose && len(stateCounts) > 0 {
		fmt.Println("  State counts:")
		stateHosts := make([]string, 0, len(stateCounts))
		for h := range stateCounts {
			stateHosts = append(stateHosts, h)
		}
		sort.Strings(stateHosts)
		for _, host := range stateHosts {
			fmt.Printf("    %s: %v\n", host, stateCounts[host])
		}
	}
}

func worker(
	ctx context.Context,
	jobs chan Job,
	results chan Result,
	wg *sync.WaitGroup,
	timeout int,
	limiter *rate.Limiter,
) {
	defer wg.Done()

	for job := range jobs {
		if err := limiter.Wait(ctx); err != nil {
			return
		}
		start := time.Now()
		addr := net.JoinHostPort(job.Target, strconv.Itoa(job.Port))
		switch job.Protocol {
		case "tcp":
			conn, err := net.DialTimeout("tcp", addr, time.Duration(timeout)*time.Second)
			if err == nil {
				conn.Close()
				results <- Result{
					Target:   job.Target,
					Port:     job.Port,
					Protocol: job.Protocol,
					Status:   "open",
					Time:     time.Since(start),
				}
			} else {
				results <- Result{
					Target:   job.Target,
					Port:     job.Port,
					Protocol: job.Protocol,
					Status:   "closed",
					Error:    err.Error(),
					Time:     time.Since(start),
				}
			}
		case "udp":
			state, errStr, rtt := scanUDP(job.Target, job.Port, time.Duration(timeout)*time.Second)
			results <- Result{
				Target:   job.Target,
				Port:     job.Port,
				Protocol: "udp",
				Status:   state,
				Error:    errStr,
				Time:     rtt,
			}
		}
	}
}

// UDP probe scan: send 1 byte, wait for reply.
// - reply => open
// - timeout/no reply => open|filtered (ambiguous)
// - "refused" sometimes indicates closed (depends on OS/network)
func scanUDP(target string, port int, timeout time.Duration) (state string, errStr string, rtt time.Duration) {
	start := time.Now()
	addr := net.JoinHostPort(target, strconv.Itoa(port))

	conn, err := net.Dial("udp", addr)
	if err != nil {
		return "error", err.Error(), time.Since(start)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(timeout))

	// Minimal probe payload (many UDP services require valid protocol messages to respond)
	if _, err := conn.Write([]byte{0x00}); err != nil {
		return "error", err.Error(), time.Since(start)
	}

	buf := make([]byte, 2048)
	_, err = conn.Read(buf)
	rtt = time.Since(start)

	if err == nil {
		return "open", "", rtt
	}

	// Timeout: ambiguous
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return "open|filtered", err.Error(), rtt
	}

	// connection refused
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "refused") {
		return "closed", err.Error(), rtt
	}

	return "open|filtered", err.Error(), rtt
}

func splitAndTrim(s string) []string {
	raw := strings.Split(s, ",")
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r != "" {
			out = append(out, r)
		}
	}
	return out
}

func parsePortRange(s string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(s), "-")
	if len(parts) == 1 {
		parts = append(parts, parts[0])
	}
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected format min-max, got %q", s)
	}
	minPort, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("min port: %w", err)
	}
	maxPort, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("max port: %w", err)
	}
	if minPort < 1 || maxPort > 65535 || minPort > maxPort {
		return 0, 0, fmt.Errorf("port range must be 1-65535 and min<=max")
	}
	return minPort, maxPort, nil
}

// returns a common service name for well-known ports.
func portServiceName(port int) string {
	names := map[int]string{
		21: "ftp", 22: "ssh", 23: "telnet", 25: "smtp", 53: "dns", 80: "http",
		110: "pop3", 143: "imap", 443: "https", 445: "smb", 993: "imaps",
		995: "pop3s", 3306: "mysql", 5432: "postgres", 6379: "redis", 8080: "http-proxy",
	}
	if name, ok := names[port]; ok {
		return name
	}
	return ""
}
