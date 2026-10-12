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

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/storage"
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
	var allocatorOpts []chromedp.ExecAllocatorOption
	if *flagShowBrowser {
		allocatorOpts = append(allocatorOpts, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck)
	} else {
		allocatorOpts = append(allocatorOpts, chromedp.Headless)
	}
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
	err := chromedp.Do(ctx,
		chromedp.Navigate(teamURL),
		chromedp.WaitVisible(".p-workspace__primary_view_contents"),
		chromedp.Func(func(ctx context.Context, _ *chromedp.Target) error {
			res, err := chromedp.Call(ctx, runtime.Evaluate, runtime.EvaluateParams{
				Expression: `q=JSON.parse(localStorage.localConfig_v2)["teams"]; q[Object.keys(q)[0]]["token"]`,
			})
			if err != nil {
				return err
			}
			if res.ExceptionDetails != nil {
				return fmt.Errorf("failed to evaluate token expression: %s", res.ExceptionDetails.Text)
			}
			if err := json.Unmarshal(res.Result.Value, &token); err != nil {
				return fmt.Errorf("failed to unmarshal token: %v", err)
			}
			return nil
		}),
		chromedp.Func(func(ctx context.Context, _ *chromedp.Target) error {
			res, err := chromedp.Call(ctx, storage.GetCookies, storage.GetCookiesParams{})
			if err != nil {
				return err
			}

			for _, c := range res.Cookies {
				if c.Name == "d" {
					cookie = fmt.Sprintf("d=%s;", c.Value)
				}
			}

			return nil
		}),
	)
	if err != nil {
		return "", "", err
	}
	return token, cookie, nil
}
