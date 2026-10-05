package commands

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/cordon-dev/cordon/command"
)

type curlCmd struct{}

func (curlCmd) Name() string { return "curl" }

type curlOptions struct {
	method   string
	headers  []string
	data     string
	outFile  string
	silent   bool
	include  bool
	location bool
	targetURL string
}

func parseCurlArgs(args []string) (curlOptions, error) {
	var opt curlOptions
	var positional []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-s" || a == "--silent":
			opt.silent = true
		case a == "-i" || a == "--include":
			opt.include = true
		case a == "-L" || a == "--location":
			opt.location = true
		case a == "-X" || a == "--request":
			i++
			if i >= len(args) {
				return opt, fmt.Errorf("option requires an argument: %s", a)
			}
			opt.method = strings.ToUpper(args[i])
		case strings.HasPrefix(a, "-X"):
			opt.method = strings.ToUpper(a[2:])
		case a == "-d" || a == "--data" || a == "--data-raw":
			i++
			if i >= len(args) {
				return opt, fmt.Errorf("option requires an argument: %s", a)
			}
			opt.data = args[i]
		case strings.HasPrefix(a, "-d"):
			opt.data = a[2:]
		case a == "-H" || a == "--header":
			i++
			if i >= len(args) {
				return opt, fmt.Errorf("option requires an argument: %s", a)
			}
			opt.headers = append(opt.headers, args[i])
		case strings.HasPrefix(a, "-H"):
			opt.headers = append(opt.headers, a[2:])
		case a == "-o" || a == "--output":
			i++
			if i >= len(args) {
				return opt, fmt.Errorf("option requires an argument: %s", a)
			}
			opt.outFile = args[i]
		case strings.HasPrefix(a, "-o"):
			opt.outFile = a[2:]
		case strings.HasPrefix(a, "-"):
			return opt, fmt.Errorf("option %s: is unknown", a)
		default:
			positional = append(positional, a)
		}
	}

	if len(positional) == 0 {
		return opt, fmt.Errorf("no URL specified")
	}
	opt.targetURL = positional[0]
	return opt, nil
}

func (curlCmd) Run(ctx context.Context, ec *command.Context) error {
	opt, err := parseCurlArgs(ec.Args[1:])
	if err != nil {
		if !opt.silent {
			ec.Errorf("curl: %v\n", err)
		}
		return command.Exit(2)
	}

	rawURL := opt.targetURL
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "http://" + rawURL
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		if !opt.silent {
			ec.Errorf("curl: invalid URL %q: %v\n", rawURL, err)
		}
		return command.Exit(2)
	}

	method := opt.method
	if method == "" {
		if opt.data != "" {
			method = "POST"
		} else {
			method = "GET"
		}
	}

	var bodyReader io.Reader
	if opt.data != "" {
		if strings.HasPrefix(opt.data, "@") {
			filePath := ec.Resolve(opt.data[1:])
			fileData, rerr := ec.FS.ReadFile(filePath)
			if rerr != nil {
				if !opt.silent {
					ec.Errorf("curl: couldn't open file %q: %s\n", opt.data[1:], errMsg(rerr))
				}
				return command.Exit(1)
			}
			bodyReader = bytes.NewReader(fileData)
		} else {
			bodyReader = strings.NewReader(opt.data)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, parsedURL.String(), bodyReader)
	if err != nil {
		if !opt.silent {
			ec.Errorf("curl: failed to create request: %v\n", err)
		}
		return command.Exit(1)
	}

	for _, h := range opt.headers {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			req.Header.Add(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		}
	}

	client := ec.Network.HTTPClient()
	if !opt.location {
		// If -L was not specified, do not follow redirects
		origCheckRedirect := client.CheckRedirect
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) > 0 {
				return http.ErrUseLastResponse
			}
			if origCheckRedirect != nil {
				return origCheckRedirect(req, via)
			}
			return nil
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		if !opt.silent {
			ec.Errorf("curl: (6) Could not resolve host or connection denied: %v\n", err)
		}
		return command.Exit(1)
	}
	defer resp.Body.Close()

	var dest io.Writer = ec.Stdout
	if opt.outFile != "" {
		resolvedOut := ec.Resolve(opt.outFile)
		// Read into buffer to write via FS
		bodyData, rerr := readAllContext(ctx, resp.Body)
		if rerr != nil {
			if !opt.silent {
				ec.Errorf("curl: error reading response: %v\n", rerr)
			}
			return command.Exit(1)
		}
		if werr := ec.FS.WriteFile(resolvedOut, bodyData, 0644); werr != nil {
			if !opt.silent {
				ec.Errorf("curl: failed writing to %s: %s\n", opt.outFile, errMsg(werr))
			}
			return command.Exit(1)
		}
		return nil
	}

	if opt.include {
		fmt.Fprintf(dest, "%s %s\r\n", resp.Proto, resp.Status)
		for k, vals := range resp.Header {
			for _, v := range vals {
				fmt.Fprintf(dest, "%s: %s\r\n", k, v)
			}
		}
		fmt.Fprint(dest, "\r\n")
	}

	_, err = copyContext(ctx, dest, resp.Body)
	if err != nil {
		if !opt.silent {
			ec.Errorf("curl: error streaming response: %v\n", err)
		}
		return command.Exit(1)
	}

	return nil
}

// Curl is the built-in HTTP client command enforcing sandboxed network policy.
var Curl command.Command = curlCmd{}
