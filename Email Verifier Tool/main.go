package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/schollz/progressbar/v3"
)

type Result struct {
	Domain    string   `json:"domain"`
	HasMX     bool     `json:"has_mx"`
	MXHosts   []string `json:"mx_hosts"`
	HasSPF    bool     `json:"has_spf"`
	SPF       string   `json:"spf_record,omitempty"`
	HasDMARC  bool     `json:"has_dmarc"`
	DMARC     string   `json:"dmarc_record,omitempty"`
	SMTPReach bool     `json:"smtp_reachable"`
	LatencyMs int64    `json:"latency_ms,omitempty"`
	Error     string   `json:"error,omitempty"`
}

func splitDomains(s string) []string {
	// split on commas and whitespace; e.g. "a,b c" -> ["a","b","c"]
	s2 := strings.ReplaceAll(s, ",", " ")
	parts := strings.Fields(s2)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func main() {
	// flags
	inFile := flag.String("file", "", "file with domains (one per line). If empty, read stdin")
	inFileShort := flag.String("f", "", "shorthand for -file")
	email := flag.String("e", "", "single domain/email to check (comma or space separated list supported)")
	format := flag.String("format", "table", "output format: table|csv|json")
	concurrency := flag.Int("concurrency", 8, "number of concurrent workers")
	rate := flag.Int("rate", 0, "rate limit (requests per second). 0 = unlimited")
	smtpCheck := flag.Bool("smtp", false, "perform lightweight SMTP EHLO check (may be slower)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Email Verifier Tool\n\n")
		fmt.Fprintf(os.Stderr, "Usage examples:\n")
		fmt.Fprintf(os.Stderr, "  echo \"example.com\" | go run main.go -format=table\n")
		fmt.Fprintf(os.Stderr, "  go run main.go -f domains.txt -format=csv -concurrency=16 > results.csv\n")
		fmt.Fprintf(os.Stderr, "  go run main.go -e example.com -smtp=true\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	var domains []string
	chosenFile := *inFile
	if chosenFile == "" {
		chosenFile = *inFileShort
	}

	fi, _ := os.Stdin.Stat()
	stdinIsTerminal := (fi.Mode() & os.ModeCharDevice) != 0

	if *email != "" {
		for _, d := range splitDomains(*email) {
			domains = append(domains, d)
		}
	}

	for _, a := range flag.Args() {
		// skip anything that looks like a flag if users provided extra tokens after a non-flag
		if strings.HasPrefix(a, "-") {
			continue
		}
		for _, d := range splitDomains(a) {
			domains = append(domains, d)
		}
	}

	if len(domains) == 0 {
		if chosenFile != "" {
			f, err := os.Open(chosenFile)
			if err != nil {
				log.Fatalf("failed to open file: %v", err)
			}
			defer f.Close()
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				t := strings.TrimSpace(scanner.Text())
				if t == "" {
					continue
				}
				domains = append(domains, t)
			}
			if err := scanner.Err(); err != nil {
				log.Fatalf("error reading input file: %v", err)
			}
		} else if !stdinIsTerminal {
			scanner := bufio.NewScanner(os.Stdin)
			for scanner.Scan() {
				t := strings.TrimSpace(scanner.Text())
				if t == "" {
					continue
				}
				domains = append(domains, t)
			}
			if err := scanner.Err(); err != nil {
				log.Fatalf("error reading stdin: %v", err)
			}
		} else {
			// no input provided and running interactively: show help
			flag.Usage()
			return
		}
	}

	total := len(domains)
	if total == 0 {
		log.Println("no domains provided")
		return
	}

	jobs := make(chan string)
	results := make(chan Result)
	var wg sync.WaitGroup

	var tick <-chan time.Time
	if *rate > 0 {
		ticker := time.NewTicker(time.Second / time.Duration(*rate))
		defer ticker.Stop()
		tick = ticker.C
	}

	// Start workers
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range jobs {
				if tick != nil {
					<-tick
				}
				r := checkDomain(d, *smtpCheck)
				results <- r
			}
		}()
	}

	// Progress bar (write to stderr so stdout table stays clean)
	bar := progressbar.NewOptions(total, progressbar.OptionSetWriter(os.Stderr))

	var outMu sync.Mutex
	collected := make([]Result, 0, total)
	done := make(chan struct{})
	go func() {
		for res := range results {
			outMu.Lock()
			collected = append(collected, res)
			outMu.Unlock()
			_ = bar.Add(1)
		}
		close(done)
	}()

	go func() {
		for _, d := range domains {
			jobs <- d
		}
		close(jobs)
	}()

	wg.Wait()
	close(results)
	<-done

	// ensure progress bar doesn't leave the cursor mid-line (puts a newline on stderr)
	fmt.Fprintln(os.Stderr)

	// Sort results by domain for deterministic output
	sort.Slice(collected, func(i, j int) bool { return collected[i].Domain < collected[j].Domain })

	// Output
	switch strings.ToLower(*format) {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(collected); err != nil {
			log.Fatalf("failed to encode json: %v", err)
		}
	case "csv":
		w := csv.NewWriter(os.Stdout)
		_ = w.Write([]string{"domain", "has_mx", "mx_hosts", "has_spf", "spf", "has_dmarc", "dmarc", "smtp_reachable", "latency_ms", "error"})
		for _, r := range collected {
			_ = w.Write([]string{
				r.Domain,
				fmt.Sprintf("%t", r.HasMX),
				strings.Join(r.MXHosts, " "),
				fmt.Sprintf("%t", r.HasSPF),
				r.SPF,
				fmt.Sprintf("%t", r.HasDMARC),
				r.DMARC,
				fmt.Sprintf("%t", r.SMTPReach),
				fmt.Sprintf("%d", r.LatencyMs),
				r.Error,
			})
		}
		w.Flush()
	default: // table
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.AppendHeader(table.Row{"Domain", "MX", "SPF", "DMARC", "SMTP", "Latency(ms)", "Error"})
		for _, r := range collected {
			var smtpCol string
			if r.SMTPReach {
				smtpCol = color.GreenString("ok")
			} else {
				smtpCol = color.RedString("no")
			}
			t.AppendRow(table.Row{r.Domain, strings.Join(r.MXHosts, ","), r.SPF, r.DMARC, smtpCol, r.LatencyMs, r.Error})
		}
		t.Render()
	}
}

func checkDomain(domain string, smtpCheck bool) Result {
	res := Result{Domain: domain}

	// MX
	mxRecords, err := net.LookupMX(domain)
	if err == nil && len(mxRecords) > 0 {
		res.HasMX = true
		for _, mx := range mxRecords {
			host := strings.TrimSuffix(mx.Host, ".")
			res.MXHosts = append(res.MXHosts, host)
		}
	}

	// TXT (SPF)
	txtRecords, err := net.LookupTXT(domain)
	if err == nil {
		for _, record := range txtRecords {
			if strings.HasPrefix(record, "v=spf1") {
				res.HasSPF = true
				res.SPF = record
				break
			}
		}
	}

	// DMARC
	dmarcDomain := "_dmarc." + domain
	dmarcTxtRecords, err := net.LookupTXT(dmarcDomain)
	if err == nil {
		for _, record := range dmarcTxtRecords {
			if strings.HasPrefix(record, "v=DMARC1") {
				res.HasDMARC = true
				res.DMARC = record
				break
			}
		}
	}

	// Lightweight SMTP check: try connecting to first MX or domain:25
	if smtpCheck {
		targets := res.MXHosts
		if len(targets) == 0 {
			targets = []string{domain}
		}
		var lastErr error
		for _, h := range targets {
			addr := net.JoinHostPort(h, "25")
			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			d := &net.Dialer{}
			conn, err := d.DialContext(ctx, "tcp", addr)
			cancel()
			if err != nil {
				lastErr = err
				continue
			}
			conn.SetDeadline(time.Now().Add(6 * time.Second))
			// simple handshake: read greet, send EHLO, read response, QUIT
			br := bufio.NewReader(conn)
			// read greet
			_, _ = br.ReadString('\n')
			fmt.Fprintf(conn, "EHLO example.com\r\n")
			// read response lines (non-blocking-ish with deadline)
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					break
				}
				if len(line) < 4 || line[3] != '-' {
					break
				}
			}
			fmt.Fprintf(conn, "QUIT\r\n")
			conn.Close()
			res.SMTPReach = true
			res.LatencyMs = time.Since(start).Milliseconds()
			lastErr = nil
			break
		}
		if lastErr != nil {
			res.Error = lastErr.Error()
		}
	}

	return res
}
