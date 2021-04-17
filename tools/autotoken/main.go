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
	"golang.org/x/term"
)

var (
	flagDebug          = pflag.BoolP("debug", "d", false, "Enable debug log")
	flagShowBrowser    = pflag.BoolP("show-browser", "b", false, "show browser, useful for debugging")
	flagChromePath     = pflag.StringP("chrome-path", "c", "", "Custom path for chrome browser")
	flagMFA            = pflag.StringP("mfa", "m", "", "Provide a multi-factor authentication token (necessary if MFA is enabled on your account)")
	flagWaitGDPRNotice = pflag.BoolP("gdpr", "g", false, "Wait for Slack's GDPR notice pop-up before inserting username and password. Use this to work around login failures")
	flagTimeout        = pflag.UintP("timeout", "t", 30, "Timeout in seconds")
)

func main() {
	usage := func() {
		fmt.Fprintf(os.Stderr, "autotoken: log into slack team and get token and cookie.\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [-d] [-mfa <token>] [-gdpr] teamname[.slack.com] email [password]\n\n", os.Args[0])
		pflag.PrintDefaults()
		os.Exit(1)
	}
	pflag.Usage = usage
	pflag.Parse()
	if len(pflag.Args()) < 2 {
		usage()
	}
	team := pflag.Arg(0)

	email := pflag.Arg(1)
	var password string
	if len(pflag.Args()) < 3 {
		// get password via terminal
		fmt.Fprintf(os.Stderr, "Enter your Slack password for user %s on team %s: ", email, team)
		pbytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			log.Fatalf("Failed to read password: %v", err)
		}
		fmt.Println()
		password = string(pbytes)
	} else {
		password = pflag.Arg(2)
	}

	timeout := time.Duration(*flagTimeout) * time.Second
	token, cookie, err := fetchCredentials(context.TODO(), team, email, password, *flagMFA, *flagWaitGDPRNotice, timeout, *flagShowBrowser, *flagDebug, *flagChromePath)
	if err != nil {
		log.Fatalf("Failed to fetch credentials for team `%s`: %v", team, err)
	}

	fmt.Printf("%s|%s\n", token, cookie)
}

// fetchCredentials fetches Slack token and cookie for a given team.
func fetchCredentials(ctx context.Context, team, email, password, mfa string, waitGDPRNotice bool, timeout time.Duration, showBrowser, doDebug bool, chromePath string) (string, string, error) {
	if !strings.HasSuffix(team, ".slack.com") {
		team += ".slack.com"
	}
	teamURL := "https://" + team

	var cancel func()
	ctx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()

	// show browser
	var allocatorOpts []chromedp.ExecAllocatorOption
	if showBrowser {
		allocatorOpts = append(allocatorOpts, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck)
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

	fmt.Fprintf(os.Stderr, "Fetching token and cookie for %s on %s\n", email, team)
	// run chromedp tasks
	return submit(ctx, teamURL, `//input[@id="email"]`, email, `//input[@id="password"]`, password, mfa, waitGDPRNotice)
}

// submit authenticates through Slack and returns token and cookie, or an error.
func submit(ctx context.Context, urlstr, selEmail, email, selPassword, password, mfa string, waitGDPRNotice bool) (string, string, error) {
	tasks := chromedp.Tasks{
		chromedp.Navigate(urlstr),
	}
	if waitGDPRNotice {
		tasks = append(tasks,
			chromedp.WaitVisible(`//*[@id="onetrust-pc-btn-handler"]`),
			// give it some time to load the JS and finish the graphical transition
			chromedp.Sleep(2*time.Second),
			chromedp.Click(`//*[@id="onetrust-pc-btn-handler"]`),
			chromedp.WaitVisible(`//*[@class="save-preference-btn-handler onetrust-close-btn-handler"]`),
			// give it some time to load the JS and finish the graphical transition
			chromedp.Sleep(2*time.Second),
			chromedp.Click(`//*[@class="save-preference-btn-handler onetrust-close-btn-handler"]`),
		)
	}
	tasks = append(tasks,
		chromedp.WaitVisible(selEmail),
		chromedp.SendKeys(selEmail, email),
		chromedp.WaitVisible(selPassword),
		chromedp.SendKeys(selPassword, password),
		chromedp.Submit(selPassword),
	)
	if err := chromedp.Run(ctx, tasks); err != nil {
		return "", "", fmt.Errorf("failed to send credentials: %w", err)
	}
	// submit MFA code if specified
	if mfa != "" {
		log.Printf("Sending MFA code")
		selMFA := `//input[@class="auth_code"]`
		mfaTasks := chromedp.Tasks{
			chromedp.WaitVisible(".auth_code"),
			chromedp.SendKeys(selMFA, mfa),
			chromedp.Submit(selMFA),
		}
		if err := chromedp.Run(ctx, mfaTasks); err != nil {
			return "", "", fmt.Errorf("failed to send MFA code: %w", err)
		}
	}

	return extractTokenAndCookie(ctx)
}

// extractTokenAndCookie extracts Slack token and cookie from an existing
// context.
func extractTokenAndCookie(ctx context.Context) (string, string, error) {
	var token, cookie string
	tasks := chromedp.Tasks{
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
