package main

/* Slack Oauth app built according to
 * https://api.slack.com/authentication/oauth-v2
 */

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
)

var (
	// irc-slack app client ID, see https://api.slack.com/apps/
	clientID     = os.Getenv("SLACK_APP_CLIENT_ID")
	clientSecret = os.Getenv("SLACK_APP_CLIENT_SECRET")
)

func httpStatus(w http.ResponseWriter, r *http.Request, statusCode int, fmtstr string, args ...interface{}) {
	w.WriteHeader(statusCode)
	msg := fmt.Sprintf(fmtstr, args...)
	fullmsg := fmt.Sprintf("%d - %s\n%s", statusCode, http.StatusText(statusCode), msg)
	if _, err := w.Write([]byte(fullmsg)); err != nil {
		log.Warningf("Cannot write response: %v", err)
	}
}

type slackChallenge struct {
	Token, Challenge, Type string
}

func handleSlackChallenge(w http.ResponseWriter, r *http.Request) {
	data, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Infof("Cannot read body: %v", err)
		httpStatus(w, r, 500, "")
		return
	}
	var sc slackChallenge
	if err := json.Unmarshal(data, &sc); err != nil {
		log.Infof("Cannot unmarshal JSON: %v", err)
		httpStatus(w, r, 400, "")
		return
	}
	if _, err := w.Write([]byte(sc.Challenge)); err != nil {
		log.Warningf("Failed to write response: %v", err)
	}
}

func handleSlackAuth(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query()["code"]
	if len(code) < 1 {
		log.Info("Missing \"code\" parameter in request")
		httpStatus(w, r, 400, "")
		return
	}
	// get token from Slack
	form := url.Values{}
	form.Add("code", code[0])
	form.Add("client_id", clientID)
	form.Add("client_secret", clientSecret)
	accessURL := "https://slack.com/api/oauth.access"

	client := http.Client{}
	req, err := http.NewRequest("POST", accessURL, strings.NewReader(form.Encode()))
	if err != nil {
		log.Infof("Failed to build request for Slack auth API: %v", err)
		httpStatus(w, r, 500, "")
		return
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		log.Infof("Failed to request token to Slack auth API: %v", err)
		httpStatus(w, r, 500, "")
		return
	}
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Infof("Failed to read body from Slack auth API response: %v", err)
		httpStatus(w, r, 500, "")
		return
	}
	// parse the response message
	// Documented at https://api.slack.com/methods/oauth.access .
	// There are other fields, but we don't care about them here.
	type authmsg struct {
		Ok           bool    `json:"ok"`
		Error        string  `json:"error"`
		AccessToken  string  `json:"access_token"`
		TeamName     string  `json:"team_name"`
		TeamID       string  `json:"team_id"`
		Scope        string  `json:"scope"`
		EnterpriseID *string `json:"enterprise_id,omitempty"`
	}
	var msg authmsg
	if err := json.Unmarshal(data, &msg); err != nil {
		log.Infof("Failed to unmarshal API response response: %v", err)
		httpStatus(w, r, 500, "")
	}
	if !msg.Ok {
		log.Infof("Got error response from auth API: %s", msg.Error)
		httpStatus(w, r, 500, "")
		return
	}
	indented, err := json.MarshalIndent(msg, "", "    ")
	if err != nil {
		log.Infof("Cannot re-marshal API response with indentation: %v", err)
		httpStatus(w, r, 500, "")
		return
	}
	httpStatus(w, r, 200, "%s", string(indented))
}

func main() {
	flag.Parse()
	addr := ":2020"
	if flag.Arg(0) != "" {
		addr = flag.Arg(0)
	}
	if clientID == "" {
		log.Fatalf("SLACK_APP_CLIENT_ID is empty or not set")
	}
	if clientSecret == "" {
		log.Fatalf("SLACK_APP_CLIENT_SECRET is empty or not set")
	}
	http.HandleFunc("/irc-slack/challenge/", handleSlackChallenge)
	http.HandleFunc("/irc-slack/auth/", handleSlackAuth)
	log.Printf("Listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
