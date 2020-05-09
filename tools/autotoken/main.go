// Command submit is a chromedp example demonstrating how to fill out and
// submit a form.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"golang.org/x/crypto/ssh/terminal"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "autotoken: log into slack team and get token and cookie.\n\nUsage: %s teamname[.slack.com] email [password]\n", os.Args[0])
		os.Exit(1)
	}
	team := os.Args[1]

	if !strings.HasSuffix(team, ".slack.com") {
		team += ".slack.com"
	}
	email := os.Args[2]
	var password string
	if len(os.Args) < 4 {
		// get password via terminal
		fmt.Fprintf(os.Stderr, "Enter your Slack password for user %s on team %s: ", email, team)
		pbytes, err := terminal.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			log.Fatalf("Failed to read password: %v", err)
		}
		fmt.Println()
		password = string(pbytes)
	} else {
		password = os.Args[3]
	}
	teamURL := "https://" + team

	ctx, cancel := chromedp.NewContext(context.Background() /*, chromedp.WithDebugf(log.Printf)*/)
	defer cancel()

	fmt.Fprintf(os.Stderr, "Fetching token and cookie for %s on %s\n", email, team)
	// run task list
	var token, cookie string
	err := chromedp.Run(ctx, submit(teamURL, `//input[@id="email"]`, email, `//input[@id="password"]`, password, &token, &cookie))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%s|%s\n", token, cookie)
}

func submit(urlstr, selEmail, email, selPassword, password string, token, cookie *string) chromedp.Tasks {
	return chromedp.Tasks{
		chromedp.Navigate(urlstr),
		chromedp.WaitVisible(selEmail),
		chromedp.SendKeys(selEmail, email),
		chromedp.Submit(selEmail),
		chromedp.WaitVisible(selPassword),
		chromedp.SendKeys(selPassword, password),
		chromedp.Submit(selPassword),
		chromedp.WaitVisible(`.p-workspace__primary_view_contents`),
		chromedp.ActionFunc(func(ctx context.Context) error {
			v, exp, err := runtime.Evaluate(`q=JSON.parse(localStorage.localConfig_v2)["teams"]; q[Object.keys(q)[0]]["token"]`).Do(ctx)
			if err != nil {
				return err
			}
			if exp != nil {
				return exp
			}
			if err := json.Unmarshal(v.Value, token); err != nil {
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
					*cookie = fmt.Sprintf("d=%s;", c.Value)
				}
			}

			return nil
		}),
	}
}
