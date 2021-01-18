// autotoken retrieves a Slack token and cookie using your Slack team
// credentials.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"golang.org/x/crypto/ssh/terminal"
)

var (
	flagDebug          = flag.Bool("d", false, "Enable debug log")
	flagShowBrowser    = flag.Bool("show-browser", false, "show browser, useful for debugging")
	flagMFA            = flag.String("mfa", "", "Provide a multi-factor authentication token (necessary if MFA is enabled on your account)")
	flagWaitGDPRNotice = flag.Bool("gdpr", false, "Wait for Slack's GDPR notice pop-up before inserting username and password. Use this to work around login failures")
	flagTimeout        = flag.Uint("t", 30, "Timeout in seconds")
)

func main() {
	usage := func() {
		fmt.Fprintf(os.Stderr, "autotoken: log into slack team and get token and cookie.\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [-d] [-mfa <token>] [-gdpr] teamname[.slack.com] email [password]\n\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(1)
	}
	flag.Usage = usage
	flag.Parse()
	if len(flag.Args()) < 2 {
		usage()
	}
	team := flag.Arg(0)

	if !strings.HasSuffix(team, ".slack.com") {
		team += ".slack.com"
	}
	email := flag.Arg(1)
	var password string
	if len(flag.Args()) < 3 {
		// get password via terminal
		fmt.Fprintf(os.Stderr, "Enter your Slack password for user %s on team %s: ", email, team)
		pbytes, err := terminal.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			log.Fatalf("Failed to read password: %v", err)
		}
		fmt.Println()
		password = string(pbytes)
	} else {
		password = flag.Arg(2)
	}
	teamURL := "https://" + team

	var opts []chromedp.ContextOption
	if *flagDebug {
		opts = append(opts, chromedp.WithDebugf(log.Printf))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*flagTimeout)*time.Second)
	defer cancel()
	// show browser
	if *flagShowBrowser {
		ctx, cancel = chromedp.NewExecAllocator(ctx, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck)
		defer cancel()
	}
	ctx, cancel = chromedp.NewContext(ctx, opts...)
	defer cancel()

	fmt.Fprintf(os.Stderr, "Fetching token and cookie for %s on %s\n", email, team)
	// run chromedp tasks
	token, cookie, err := submit(ctx, teamURL, `//input[@id="email"]`, email, `//input[@id="password"]`, password, *flagMFA, *flagWaitGDPRNotice)
	if err != nil {
		log.Fatalf("Failed to get Slack token and cookie: %v", err)
	}

	fmt.Printf("%s|%s\n", token, cookie)
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
