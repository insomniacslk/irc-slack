// autotoken retrieves a Slack token and cookie using your Slack team
// credentials.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/spf13/pflag"
)

var (
	flagDebug       = pflag.BoolP("debug", "d", false, "Enable debug log")
	flagShowBrowser = pflag.BoolP("show-browser", "b", false, "show browser, useful for debugging")
	flagChromePath  = pflag.StringP("chrome-path", "c", "", "Custom path for chrome browser")
	flagTimeout     = pflag.DurationP("timeout", "t", 5*time.Minute, "Timeout")
)

func main() {
	usage := func() {
		fmt.Fprintf(os.Stderr, "autotoken: log into slack team and get token and cookie.\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [-d|--debug] [-m|--mfa <token>] [-g|--gdpr] teamname[.slack.com]\n\n", os.Args[0])
		pflag.PrintDefaults()
		os.Exit(1)
	}
	pflag.Usage = usage
	pflag.Parse()
	if len(pflag.Args()) < 1 {
		usage()
	}
	team := pflag.Arg(0)

	timeout := *flagTimeout
	token, cookie, err := fetchCredentials(context.TODO(), team, timeout, *flagDebug, *flagChromePath)
	if err != nil {
		log.Fatalf("Failed to fetch credentials for team `%s`: %v", team, err)
	}

	fmt.Printf("%s|%s\n", token, cookie)
}

// fetchCredentials fetches Slack token and cookie for a given team.
func fetchCredentials(ctx context.Context, team string, timeout time.Duration, doDebug bool, chromePath string) (string, string, error) {
	if !strings.HasSuffix(team, ".slack.com") {
		team += ".slack.com"
	}

	var cancel func()
	ctx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()

	// show browser
	allocatorOpts := append([]chromedp.ExecAllocatorOption{}, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck)
	if chromePath != "" {
		allocatorOpts = append(allocatorOpts, chromedp.ExecPath(chromePath))
	}
	ctx, cancel = chromedp.NewExecAllocator(ctx, allocatorOpts...)
	defer cancel()

	var opts []chromedp.ContextOption
	if doDebug {
		opts = append(opts, chromedp.WithDebugf(log.Printf))
	}

	ctx, cancel = chromedp.NewContext(ctx, opts...)
	defer cancel()

	fmt.Fprintf(os.Stderr, "Fetching token and cookie for %s \n", team)
	// run chromedp tasks
	return extractTokenAndCookie(ctx, team)
}

// extractTokenAndCookie extracts Slack token and cookie from an existing
// context.
func extractTokenAndCookie(ctx context.Context, team string) (string, string, error) {
	teamURL := "https://" + team
	var token, cookie string
	tasks := chromedp.Tasks{
		chromedp.Navigate(teamURL),
		chromedp.WaitVisible(".p-workspace__primary_view_contents"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			v, exp, err := runtime.Evaluate(`q=JSON.parse(localStorage.localConfig_v2)["teams"]; q[Object.keys(q)[0]]["token"]`).Do(ctx)
			if err != nil {
				return err
			}
			if exp != nil {
				return exp
			}
			if err := json.Unmarshal(v.Value, &token); err != nil {
				return fmt.Errorf("failed to unmarshal token: %v", err)
			}
			return nil
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			cookies, err := network.GetAllCookies().Do(ctx)
			if err != nil {
				return err
			}

			for _, c := range cookies {
				if c.Name == "d" {
					cookie = fmt.Sprintf("d=%s;", c.Value)
				}
			}

			return nil
		}),
	}
	if err := chromedp.Run(ctx, tasks); err != nil {
		return "", "", err
	}
	return token, cookie, nil
}
