# Port Scanner

A concurrent TCP/UDP port scanner written in Go. Scans one or more IP addresses over a port range and reports open ports with optional service names.

## Requirements

- Go 1.21 or later (for building from source)

## Build

```bash
go get golang.org/x/time/rate
go build -o port-scanner .
```

## Usage

```bash
./port-scanner [options]
```

### Options

| Flag        | Default     | Description                                      |
|------------|-------------|--------------------------------------------------|
| `-targets` | `127.0.0.1` | Target IP(s), comma-separated (e.g. `192.168.1.1,10.0.0.1`) |
| `-port`    | `1-1024`    | Port range as `min-max` or single port (e.g. `80`, `1-1000`) |
| `-timeout` | `2`         | Timeout per connection in seconds                |
| `-workers` | `100`       | Number of concurrent workers                     |
| `-protocol`| `tcp`       | Protocol: `tcp` or `udp`                         |
| `-verbose` | `false`     | Show closed/filtered results and state counts   |
| `-rate`    | `0`         | Max probes per second (0 = no limit)            |

**Note:** Targets must be valid IP addresses (IPv4 or IPv6). Hostnames are not supported.

### Examples

**Scan localhost, default ports 1–1024 (TCP):**
```bash
./port-scanner
```

**Scan a single host, common web ports:**
```bash
./port-scanner -targets=192.168.1.1 -port=80-443
```

**Scan multiple IPs:**
```bash
./port-scanner -targets=192.168.1.1,192.168.1.2,10.0.0.1 -port=22,80,443,3306
```

**Single port:**
```bash
./port-scanner -targets=127.0.0.1 -port=8080
```

**Faster scan (shorter timeout, more workers):**
```bash
./port-scanner -targets=192.168.1.1 -port=1-1000 -timeout=1 -workers=200
```

**Rate-limited scan (e.g. max 50 probes per second):**
```bash
./port-scanner -targets=192.168.1.1 -port=1-1000 -rate=50
```

**Verbose output (show closed/filtered and per-host state counts):**
```bash
./port-scanner -targets=127.0.0.1 -port=1-100 -verbose
```

**UDP scan:**
```bash
./port-scanner -targets=127.0.0.1 -port=53,67,68 -protocol=udp -timeout=3
```

**IPv6:**
```bash
./port-scanner -targets=::1 -port=80,443
```

### Interrupt

Press **Ctrl+C** to stop the scan. The program exits after printing a message; some results may be incomplete.

## Output

**Live results** (while scanning):

- A header line: `STATUS`, `TARGET:PORT`, `PROTO`, `RTT`
- One line per open port (and per closed/filtered port when `-verbose` is set)
- RTT is rounded to milliseconds

**Summary** (after the scan or interrupt):

- Total duration and number of ports scanned
- Total open port count
- **Open ports by host:** each host with a sorted list of open ports; well-known ports show a service name (e.g. `22 (ssh)`, `80 (http)`)

With **`-verbose`**:

- **State counts:** per-host counts of open, closed, open|filtered, etc.

### Example summary

```
====================================================
  Scan complete
====================================================
  Duration: 1.234s
  Total:    1024 scanned
  Open:     3 port(s)

  Open ports by host:
    127.0.0.1: 22 (ssh), 80 (http), 443 (https)
```

## Port range

- Valid range: **1–65535**
- Format: `min-max` (e.g. `1-1024`) or single port (e.g. `80`)
- `min` must be ≤ `max`

## Protocol notes

- **TCP:** Connect-based; open = connection succeeded, closed = connection refused or failed.
- **UDP:** Probe-based; open = reply received, timeout/no reply = reported as `open|filtered` (ambiguous). Some systems report “connection refused” for closed UDP ports.

## License

Use and modify as you like.
