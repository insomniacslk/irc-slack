package main

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coredhcp/coredhcp/logger"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

// Project constants
const (
	ProjectAuthor       = "Andrea Barberio"
	ProjectAuthorEmail  = "insomniac@slackware.it"
	ProjectURL          = "https://github.com/insomniacslk/irc-slack"
	MaxSlackAPIAttempts = 3
)

// IrcCommandHandler is the prototype that every IRC command handler has to implement
type IrcCommandHandler func(*IrcContext, string, string, []string, string)

// IrcCommandHandlers maps each IRC command to its handler function
var IrcCommandHandlers = map[string]IrcCommandHandler{
	"CAP":     IrcCapHandler,
	"NICK":    IrcNickHandler,
	"USER":    IrcUserHandler,
	"PING":    IrcPingHandler,
	"PRIVMSG": IrcPrivMsgHandler,
	"QUIT":    IrcQuitHandler,
	"MODE":    IrcModeHandler,
	"PASS":    IrcPassHandler,
	"WHOIS":   IrcWhoisHandler,
	"WHO":     IrcWhoHandler,
	"JOIN":    IrcJoinHandler,
	"PART":    IrcPartHandler,
	"TOPIC":   IrcTopicHandler,
}

// IrcNumericsSafeToChunk is a list of IRC numeric replies that are safe
// to chunk. As per RFC2182, the maximum message size is 512, including
// newlines. Sending longer lines breaks some clients like ZNC. See
// https://github.com/insomniacslk/irc-slack/issues/38 for background.
// This list is meant to grow if we find more IRC numerics that are safe
// to split.
// Being safe to split doesn't mean that it *will* be split. The actual
// behaviour depends on the IrcContext.ChunkSize value.
var IrcNumericsSafeToChunk = []int{
	// RPL_WHOREPLY
	352,
	// RPL_NAMREPLY
	353,
}

// SplitReply will split a reply message if necessary. See
// IrcNumericSafeToChunk for background on why splitting.
// The function will return a list of chunks to be sent
// separately.
// The first argument is the entire message to be split.
// The second argument is the chunk size to use to determine
// whether the message should be split. Any value equal or above
// 512 will cause splitting. Any other value will return the
// unmodified string as only item of the list.
func SplitReply(preamble, msg string, chunksize int) []string {
	if chunksize < 512 || chunksize >= len(preamble)+len(msg)+2 {
		// return the whole string as one chunk
		return []string{preamble + msg + "\r\n"}
	}
	log.Debugf("Splitting reply in %d-byte chunks", chunksize)
	// Split and build a string until it's long enough to fit the
	// chunk. Splitting ignores multiple contiguous white-spaces.
	// We assume this is safe (unless we find out it's not).
	// Additionally, squeezing multiple contiguous spaces could
	// render the final reply shorter than the chunk size, but we
	// don't care here.
	maxLen := chunksize - len(preamble) - 2
	lines := WordWrap(strings.Fields(msg), maxLen)
	reply := make([]string, len(lines))
	for idx, line := range lines {
		reply[idx] = preamble + line + "\r\n"
	}
	return reply
}

var (
	rxSlackUrls       = regexp.MustCompile(`<[^>]+>?`)
	rxSlackUser       = regexp.MustCompile(`<@[UW][A-Z0-9]+>`)
	rxSlackArchiveURL = regexp.MustCompile(`https?:\\/\\/[a-z0-9\\-]+\\.slack\\.com\\/archives\\/([a-zA-Z0-9]+)\\/p([0-9]{10})([0-9]{6})`)
)

// ExpandText expands and unquotes text and URLs from Slack's messages. Slack
// quotes the text and URLS, and the latter are enclosed in < and >. It also
// translates potential URLs into actual URLs (e.g. when you type "example.com"),
// so you will get something like <http://example.com|example.com>. This
// function tries to detect them and unquote and expand them for a better
// visualization on IRC.
func ExpandText(text string) string {

	text = rxSlackUrls.ReplaceAllStringFunc(text, func(subs string) string {
		if !strings.HasPrefix(subs, "<") && !strings.HasSuffix(subs, ">") {
			return subs
		}

		// Slack URLs may contain an URL followed by a "|", followed by the
		// original message. Detect the pipe and only parse the URL.
		var (
			slackURL = subs[1 : len(subs)-1]
			slackMsg string
		)
		idx := strings.LastIndex(slackURL, "|")
		if idx >= 0 {
			slackMsg = slackURL[idx+1:]
			slackURL = slackURL[:idx]
		}

		u, err := url.Parse(slackURL)
		if err != nil {
			return subs
		}
		// Slack escapes the URLs passed by the users, let's undo that
		//u.RawQuery = html.UnescapeString(u.RawQuery)
		if slackMsg == "" {
			return u.String()
		}
		return fmt.Sprintf("%s (%s)", slackMsg, u.String())
	})
	text = html.UnescapeString(text)
	return text
}

// SendIrcNumeric sends a numeric code message to the recipient
func SendIrcNumeric(ctx *IrcContext, code int, args, desc string) error {
	preamble := fmt.Sprintf(":%s %03d %s :", ctx.ServerName, code, args)
	//reply := fmt.Sprintf(":%s %03d %s :%s\r\n", ctx.ServerName, code, args, desc)
	chunks := SplitReply(preamble, desc, ctx.ChunkSize)
	for _, chunk := range chunks {
		log.Debugf("Sending numeric reply: %s", chunk)
		_, err := ctx.Conn.Write([]byte(chunk))
		if err != nil {
			return err
		}
	}
	return nil
}

// IrcSendChanInfoAfterJoin sends channel information to the user about a joined
// channel.
func IrcSendChanInfoAfterJoin(ctx *IrcContext, name, id, topic string, members []string, isGroup bool) {
	// TODO wrap all these Conn.Write into a function
	if _, err := ctx.Conn.Write([]byte(fmt.Sprintf(":%v JOIN %v\r\n", ctx.Mask(), name))); err != nil {
		log.Warningf("Failed to send IRC message: %v", err)
	}
	// RPL_TOPIC
	if err := SendIrcNumeric(ctx, 332, fmt.Sprintf("%s %s", ctx.Nick(), name), topic); err != nil {
		log.Warningf("Failed to send IRC message: %v", err)
	}
	// RPL_NAMREPLY
	if err := SendIrcNumeric(ctx, 353, fmt.Sprintf("%s = %s", ctx.Nick(), name), strings.Join(ctx.UserIDsToNames(members...), " ")); err != nil {
		log.Warningf("Failed to send IRC message: %v", err)
	}
	// RPL_ENDOFNAMES
	if err := SendIrcNumeric(ctx, 366, fmt.Sprintf("%s %s", ctx.Nick(), name), "End of NAMES list"); err != nil {
		log.Warningf("Failed to send IRC message: %v", err)
	}
	ctx.ChanMutex.Lock()
	ctx.Channels[name] = Channel{Topic: topic, Members: members, ID: id, IsGroup: isGroup}
	log.Infof("Joined channel %s: %+v", name, ctx.Channels[name])
	ctx.ChanMutex.Unlock()
}

func usersInConversation(ctx *IrcContext, conversation string) ([]string, error) {
	var (
		members, m []string
		nextCursor string
		err        error
		page       int
	)
	for {
		attempt := 0
		for {
			// retry if rate-limited, no more than MaxSlackAPIAttempts times
			if attempt >= MaxSlackAPIAttempts {
				return nil, fmt.Errorf("GetUsersInConversation: exceeded the maximum number of attempts (%d) with the Slack API", MaxSlackAPIAttempts)
			}
			log.Debugf("GetUsersInConversation: page %d attempt #%d nextCursor=%s", page, attempt, nextCursor)
			m, nextCursor, err = ctx.SlackClient.GetUsersInConversation(&slack.GetUsersInConversationParameters{ChannelID: conversation, Cursor: nextCursor})
			if err != nil {
				log.Errorf("Failed to get users in conversation '%s': %v", conversation, err)
				if rlErr, ok := err.(*slack.RateLimitedError); ok {
					// we were rate-limited. Let's wait as much as Slack
					// instructs us to do
					log.Warningf("Hit Slack API rate limiter. Waiting %v", rlErr.RetryAfter)
					time.Sleep(rlErr.RetryAfter)
					attempt++
					continue
				}
				return nil, fmt.Errorf("Cannot get member list for conversation %s: %v", conversation, err)
			}
			break
		}
		members = append(members, m...)
		if nextCursor == "" {
			break
		}
		page++
	}
	return members, nil
}

// join will join the channel with the given ID, name and topic, and send back a
// response to the IRC client
func join(ctx *IrcContext, id, name, topic string) error {
	members, err := usersInConversation(ctx, id)
	if err != nil {
		return err
	}
	info := fmt.Sprintf("#%s topic=%s members=%d", name, topic, len(members))
	log.Infof(info)
	// the channels are already joined, notify the IRC client of their
	// existence
	go IrcSendChanInfoAfterJoin(ctx, name, id, topic, members, false)
	return nil
}

// joinChannels gets all the available Slack channels and sends an IRC JOIN message
// for each of the joined channels on Slack
func joinChannels(ctx *IrcContext) error {
	log.Info("Channel list:")
	var (
		channels, chans []slack.Channel
		nextCursor      string
		err             error
	)
	for {
		attempt := 0
		for {
			// retry if rate-limited, no more than MaxSlackAPIAttempts times
			if attempt >= MaxSlackAPIAttempts {
				return fmt.Errorf("GetConversations: exceeded the maximum number of attempts (%d) with the Slack API", MaxSlackAPIAttempts)
			}
			log.Infof("GetConversations: attempt #%d, nextCursor=%s", attempt, nextCursor)
			params := slack.GetConversationsParameters{
				Types:  []string{"public_channel", "private_channel"},
				Cursor: nextCursor,
			}
			chans, nextCursor, err = ctx.SlackClient.GetConversations(&params)
			if err != nil {
				log.Warningf("Failed to get conversations: %v", err)
				if rlErr, ok := err.(*slack.RateLimitedError); ok {
					// we were rate-limited. Let's wait as much as Slack
					// instructs us to do
					log.Warningf("Hit Slack API rate limiter. Waiting %v", rlErr.RetryAfter)
					time.Sleep(rlErr.RetryAfter)
					attempt++
					continue
				}
				return fmt.Errorf("Cannot get slack channels: %v", err)
			}
			break
		}
		channels = append(channels, chans...)
		if nextCursor == "" {
			break
		}
	}
	for _, ch := range channels {
		if ch.IsMember {
			if err := join(ctx, ch.ID, "#"+ch.Name, ch.Purpose.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

// IrcAfterLoggingIn is called once the user has successfully logged on IRC
func IrcAfterLoggingIn(ctx *IrcContext, rtm *slack.RTM) error {
	// Send a welcome to the user, to let the client knows that it's connected
	// RPL_WELCOME
	if err := SendIrcNumeric(ctx, 1, ctx.Nick(), fmt.Sprintf("Welcome to the %s IRC chat, %s!", ctx.ServerName, ctx.Nick())); err != nil {
		log.Warningf("Failed to send IRC message: %v", err)
	}
	// RPL_MOTDSTART
	if err := SendIrcNumeric(ctx, 375, ctx.Nick(), ""); err != nil {
		log.Warningf("Failed to send IRC message: %v", err)
	}
	// RPL_MOTD
	motd := func(s string) {
		if err := SendIrcNumeric(ctx, 372, ctx.Nick(), s); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
	}
	motd(fmt.Sprintf("This is an IRC-to-Slack gateway, written by %s <%s>.", ProjectAuthor, ProjectAuthorEmail))
	motd(fmt.Sprintf("More information at %s.", ProjectURL))
	motd(fmt.Sprintf("Slack team name: %s", ctx.SlackRTM.GetInfo().Team.Name))
	motd(fmt.Sprintf("Your user info: "))
	motd(fmt.Sprintf("  Name     : %s", ctx.User.Name))
	motd(fmt.Sprintf("  ID       : %s", ctx.User.ID))
	motd(fmt.Sprintf("  RealName : %s", ctx.User.RealName))
	// RPL_ENDOFMOTD
	if err := SendIrcNumeric(ctx, 376, ctx.Nick(), ""); err != nil {
		log.Warningf("Failed to send IRC message: %v", err)
	}

	ctx.Channels = make(map[string]Channel)
	ctx.ChanMutex = &sync.Mutex{}

	// get channels
	if err := joinChannels(ctx); err != nil {
		return err
	}

	go eventHandler(ctx, rtm)
	return nil
}

// IrcCapHandler is called when a CAP command is sent
func IrcCapHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) > 1 {
		if args[0] == "LS" {
			reply := fmt.Sprintf(":%s CAP * LS :\r\n", ctx.ServerName)
			if _, err := ctx.Conn.Write([]byte(reply)); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
		} else {
			log.Debugf("Got CAP %v", args)
		}
	}
}

// parseMentions parses mentions and converts them to the syntax that
// Slack will parse, i.e. <@nickname>
func parseMentions(text string) string {
	tokens := strings.Split(text, " ")
	for idx, token := range tokens {
		if token == "@here" {
			tokens[idx] = "<!here>"
		} else if token == "@channel" {
			tokens[idx] = "<!channel>"
		} else if token == "@everyone" {
			tokens[idx] = "<!everyone>"
		} else if strings.HasPrefix(token, "@") {
			tokens[idx] = "<" + token + ">"
		}
	}
	return strings.Join(tokens, " ")
}

func getTargetTs(channelName string) string {
	if !strings.HasPrefix(channelName, "+") {
		return ""
	}
	chanNameSplit := strings.Split(channelName, "-")
	return chanNameSplit[len(chanNameSplit)-1]
}

// IrcPrivMsgHandler is called when a PRIVMSG command is sent
func IrcPrivMsgHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	channelParameter := ""
	text := ""
	if len(args) == 1 {
		channelParameter = args[0]
		text = trailing
	} else if len(args) == 2 {
		channelParameter = args[0]
		text = args[1]
	}
	if channelParameter == "" || text == "" {
		log.Warningf("Invalid PRIVMSG command args: %v %v", args, trailing)
		return
	}
	channel, ok := ctx.Channels[channelParameter]
	target := ""
	if ok {
		target = channel.ID
	} else {
		target = "@" + channelParameter
	}

	if strings.HasPrefix(text, "\x01ACTION ") && strings.HasSuffix(text, "\x01") {
		// The Slack API has a bug, where a chat.meMessage is
		// documented to accept a channel name or ID, but actually
		// only the channel ID will work. So until this is fixed,
		// resolve the channel ID for chat.meMessage .
		// TODO revert this when the bug in the Slack API is fixed
		key := target
		ch, ok := ctx.Channels[key]
		if !ok {
			log.Warningf("Unknown channel ID for %s", key)
			return
		}
		target = ch.ID

		// this is a MeMessage
		// strip off the ACTION and \x01 wrapper
		text = text[len("\x01ACTION ") : len(text)-1]
		/*
		 * workaround: I believe that there is an issue with the
		 * slack API for the method chat.meMessage . Until this
		 * is clarified, I will emulate a "me message" using a
		 * simple italic formatting for the message.
		 * See https://github.com/insomniacslk/irc-slack/pull/39
		 */
		// TODO once clarified the issue, restore the
		//      MsgOptionMeMessage, remove the MsgOptionAsUser,
		//      and remove the italic text
		//opts = append(opts, slack.MsgOptionMeMessage())
		text = "_" + text + "_"
	}
	ctx.PostTextMessage(
		target,
		parseMentions(text),
		getTargetTs(channelParameter),
	)
}

// wrapped logger that satisfies the slack.logger interface
type loggerWrapper struct {
	*logrus.Entry
}

func (l *loggerWrapper) Output(calldepth int, s string) error {
	l.Print(s)
	return nil
}

// custom HTTP client used to set the auth cookie if requested, and only over
// TLS.
type httpClient struct {
	c      http.Client
	cookie string
}

func (hc httpClient) Do(req *http.Request) (*http.Response, error) {
	if hc.cookie != "" {
		log.Debugf("Setting auth cookie")
		if strings.ToLower(req.URL.Scheme) == "https" {
			req.Header.Add("Cookie", hc.cookie)
		} else {
			log.Warning("Cookie is set but connection is not HTTPS, skipping")
		}
	}
	return hc.c.Do(req)
}

// passwordToTokenAndCookie parses the password specified by the user into a
// Slack token and optionally a cookie Auth cookies can be specified by
//appending a "|" symbol and the base64-encoded auth cookie to the Slack token.
func passwordToTokenAndCookie(p string) (string, string, error) {
	parts := strings.Split(p, "|")

	switch len(parts) {
	case 1:
		// XXX should check that the token starts with xoxp- ?
		return parts[0], "", nil
	case 2:
		if !strings.HasPrefix(parts[0], "xoxc-") {
			return "", "", errors.New("auth cookie is set, but token does not start with xoxc-")
		}
		if parts[1] == "" {
			return "", "", errors.New("auth cookie is empty")
		}
		if !strings.HasPrefix(parts[1], "d=") || !strings.HasSuffix(parts[1], ";") {
			return "", "", errors.New("auth cookie must have the format 'd=XXX;'")
		}
		return parts[0], parts[1], nil
	default:
		return "", "", fmt.Errorf("failed to parse password into token and cookie, got %d components, want 1 or 2", len(parts))
	}
}

func connectToSlack(ctx *IrcContext) error {
	token, cookie, err := passwordToTokenAndCookie(ctx.SlackAPIKey)
	if err != nil {
		return err
	}
	ctx.SlackClient = slack.New(
		token,
		slack.OptionDebug(ctx.SlackDebug),
		slack.OptionLog(&loggerWrapper{logger.GetLogger("slack-api")}),
		slack.OptionHTTPClient(&httpClient{cookie: cookie}),
	)
	rtm := ctx.SlackClient.NewRTM()
	ctx.SlackRTM = rtm
	go rtm.ManageConnection()
	log.Info("Starting Slack client")
	// Wait until the websocket is connected, then print client info
	var info *slack.Info
	// FIXME tune the timeout to a value that makes sense
	timeout := 10 * time.Second
	start := time.Now()
	for {
		if info = rtm.GetInfo(); info != nil {
			break
		}
		if time.Now().After(start.Add(timeout)) {
			return fmt.Errorf("Connection to Slack timed out after %v", timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.Info("CLIENT INFO:")
	log.Infof("  URL     : %s", info.URL)
	log.Infof("  User    : %+v", *info.User)
	log.Infof("  Team    : %+v", *info.Team)
	user := ctx.GetUserInfo(info.User.ID)
	if user == nil {
		return fmt.Errorf("Cannot get info for user %s (ID: %s)", info.User.Name, info.User.ID)
	}
	ctx.User = user
	ctx.RealName = user.RealName
	return IrcAfterLoggingIn(ctx, rtm)
}

// IrcNickHandler is called when a NICK command is sent
func IrcNickHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	nick := trailing
	if len(args) == 1 {
		nick = args[0]
	}
	if nick == "" {
		log.Warningf("Invalid NICK command args: %v %v", args, trailing)
		return
	}
	// The nickname cannot be changed here. Just set it to whatever Slack says
	// you are.
	if ctx.SlackClient == nil {
		if err := connectToSlack(ctx); err != nil {
			log.Warningf("Cannot connect to Slack: %v", err)
			// close the IRC connection to the client
			ctx.Conn.Close()
		}
	}
	// ctx.SlackRTM.GetInfo() should not be `nil` at this points. If it is, it's ok
	// to panic here
	if nick != ctx.Nick() {
		// the client is trying to use a different nickname, let's tell them
		// they can't.
		// RPL_SAVENICK
		if err := SendIrcNumeric(
			ctx, 43, nick,
			fmt.Sprintf("Your nickname is %s and cannot be changed", ctx.Nick()),
		); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
	}
	log.Infof("Setting nickname for %v to %v", ctx.Conn.RemoteAddr(), ctx.Nick())
}

// IrcUserHandler is called when a USER command is sent
func IrcUserHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if ctx.Nick() == "" {
		log.Warning("Empty nickname!")
		return
	}
	// ignore the user-specified username. Will use the Slack ID instead
	// TODO get user info and set the real name with that info
	ctx.RealName = trailing
}

// IrcPingHandler is called when a PING command is sent
func IrcPingHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	msg := fmt.Sprintf("PONG %s", strings.Join(args, " "))
	if trailing != "" {
		msg += " :" + trailing
	}
	if _, err := ctx.Conn.Write([]byte(msg + "\r\n")); err != nil {
		log.Warningf("Failed to send IRC message: %v", err)
	}
}

// IrcQuitHandler is called when a QUIT command is sent
func IrcQuitHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	ctx.Conn.Close()
}

// IrcModeHandler is called when a MODE command is sent
func IrcModeHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	switch len(args) {
	case 0:
		log.Warningf("Invalid call to MODE handler: no arguments passed")
	case 1:
		// get mode request. Always no mode (for now)
		mode := "+"
		// RPL_CHANNELMODEIS
		if err := SendIrcNumeric(ctx, 324, fmt.Sprintf("%s %s %s", ctx.Nick(), args[0], mode), ""); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
	default:
		// more than 1
		// set mode request. Not handled yet
		// TODO handle mode set
		// ERR_UMODEUNKNOWNFLAG
		if err := SendIrcNumeric(ctx, 501, args[0], fmt.Sprintf("Unknown MODE flags %s", strings.Join(args[1:], " "))); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
	}
}

// IrcPassHandler is called when a PASS command is sent
func IrcPassHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) != 1 {
		log.Warningf("Invalid PASS arguments. Arguments are not shown for this method because they may contain Slack tokens or cookies")
		// ERR_PASSWDMISMATCH
		if err := SendIrcNumeric(ctx, 464, "", "Invalid password"); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		return
	}
	ctx.SlackAPIKey = args[0]
	ctx.FileHandler.SlackAPIKey = ctx.SlackAPIKey
}

// IrcWhoHandler is called when a WHO command is sent
func IrcWhoHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	sendErr := func() {
		// ERR_UNKNOWNERROR
		if err := SendIrcNumeric(ctx, 400, ctx.Nick(), "Invalid WHO command. Syntax: WHO <nickname|channel>"); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
	}
	if len(args) != 1 && len(args) != 2 {
		sendErr()
		return
	}
	target := args[0]
	var rargs, desc string
	if strings.HasPrefix(target, "#") {
		ch, ok := ctx.Channels[target]
		if !ok {
			// ERR_NOSUCHCHANNEL
			if err := SendIrcNumeric(ctx, 403, ctx.Nick(), fmt.Sprintf("No such channel %s", target)); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
			return
		}
		for _, un := range ch.Members {
			// FIXME can we use the cached users?
			u := ctx.GetUserInfo(un)
			if u == nil {
				log.Warningf("Failed to get info for user name '%s'", un)
				continue
			}
			log.Infof("%+v", u.Name)
			rargs = fmt.Sprintf("%s %s %s %s %s *", target, u.ID, ctx.ServerName, ctx.ServerName, u.Name)
			desc = fmt.Sprintf("0 %s", u.RealName)
			// RPL_WHOREPLY
			if err := SendIrcNumeric(ctx, 352, rargs, desc); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
		}
		// RPL_ENDOFWHO
		if err := SendIrcNumeric(ctx, 315, target, "End of WHO list"); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		return
	}
	user := ctx.GetUserInfoByName(target)
	if user == nil {
		// ERR_NOSUCHNICK
		if err := SendIrcNumeric(ctx, 401, ctx.Nick(), fmt.Sprintf("No such nick %s", target)); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		return
	}
	rargs = fmt.Sprintf("#general %s %s %s %s *", user.ID, ctx.ServerName, ctx.ServerName, user.Name)
	desc = fmt.Sprintf("0 %s", user.RealName)
	// RPL_WHOREPLY
	if err := SendIrcNumeric(ctx, 352, rargs, desc); err != nil {
		log.Warningf("Failed to send IRC message: %v", err)
	}
}

// IrcWhoisHandler is called when a WHOIS command is sent
func IrcWhoisHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) != 1 && len(args) != 2 {
		// ERR_UNKNOWNERROR
		if err := SendIrcNumeric(ctx, 400, ctx.Nick(), "Invalid WHOIS command. Syntax: WHOIS <username>"); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		return
	}
	username := args[0]
	// if the second argument is the same as the first, it's a request of WHOIS
	// with idle time
	withIdleTime := false
	if len(args) == 2 && args[0] == args[1] {
		withIdleTime = true
	}
	user := ctx.GetUserInfoByName(username)
	if user == nil {
		// ERR_NOSUCHNICK
		if err := SendIrcNumeric(ctx, 401, ctx.Nick(), fmt.Sprintf("No such nick %s", username)); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
	} else {
		// RPL_WHOISUSER
		if err := SendIrcNumeric(ctx, 311, fmt.Sprintf("%s %s %s %s *", username, user.Name, user.ID, ctx.ServerName), user.RealName); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		// RPL_WHOISSERVER
		if err := SendIrcNumeric(ctx, 312, fmt.Sprintf("%s %s", username, ctx.ServerName), ctx.ServerName); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		// RPL_WHOISCHANNELS
		// TODO get the user's channels
		channels := []string{}
		if err := SendIrcNumeric(ctx, 319, ctx.Nick(), fmt.Sprintf("%s %s", username, strings.Join(channels, " "))); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		if withIdleTime {
			// TODO send RPL_WHOISIDLE (317)
		}
		// RPL_ENDOFWHOIS
		if err := SendIrcNumeric(ctx, 318, ctx.Nick(), username); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
	}
}

// IrcJoinHandler is called when a JOIN command is sent
func IrcJoinHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) != 1 {
		// ERR_UNKNOWNERROR
		if err := SendIrcNumeric(ctx, 400, ctx.Nick(), "Invalid JOIN command"); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		return
	}
	// Because it is possible for an IRC Client to join multiple channels
	// via a multi join (e.g. /join #chan1,#chan2,#chan3) the argument
	// needs to be splitted by commas and each channel needs to be joined
	// separately.
	channames := strings.Split(args[0], ",")
	for _, channame := range channames {
		if strings.HasPrefix(channame, "&") || strings.HasPrefix(channame, "+") {
			continue
		}
		ch, err := ctx.SlackClient.JoinChannel(channame)
		if err != nil {
			log.Warningf("Cannot join channel %s: %v", channame, err)
			continue
		}
		log.Infof("Joined channel %s", channame)
		go IrcSendChanInfoAfterJoin(ctx, channame, ch.ID, ch.Purpose.Value, ch.Members, true)
	}
}

// IrcPartHandler is called when a PART command is sent
func IrcPartHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) != 1 {
		// ERR_UNKNOWNERROR
		if err := SendIrcNumeric(ctx, 400, ctx.Nick(), "Invalid PART command"); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		return
	}
	channame := strings.TrimPrefix(args[0], "#")
	// Slack needs the channel ID to leave it, not the channel name. The only
	// way to get the channel ID from the name is retrieving the whole channel
	// list and finding the one whose name is the one we want to leave
	chanlist, err := ctx.SlackClient.GetChannels(true)
	if err != nil {
		log.Warningf("Cannot leave channel %s: %v", channame, err)
		// ERR_UNKNOWNERROR
		if err := SendIrcNumeric(ctx, 400, ctx.Nick(), fmt.Sprintf("Cannot leave channel: %v", err)); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		return
	}
	var chanID string
	for _, ch := range chanlist {
		if ch.Name == channame {
			chanID = ch.ID
			log.Debugf("Trying to leave channel: %+v", ch)
			break
		}
	}
	if chanID == "" {
		// ERR_USERNOTINCHANNEL
		if err := SendIrcNumeric(ctx, 441, ctx.Nick(), fmt.Sprintf("User is not in channel %s", channame)); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
			return
		}
		notInChan, err := ctx.SlackClient.LeaveChannel(chanID)
		if err != nil {
			log.Warningf("Cannot leave channel %s (id: %s): %v", channame, chanID, err)
			return
		}
		if notInChan {
			// ERR_USERNOTINCHANNEL
			if err := SendIrcNumeric(ctx, 441, ctx.Nick(), fmt.Sprintf("User is not in channel %s", channame)); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
			return
		}
		log.Debugf("Left channel %s", channame)
		if _, err := ctx.Conn.Write([]byte(fmt.Sprintf(":%v PART #%v\r\n", ctx.Mask(), channame))); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
	}
}

// IrcTopicHandler is called when a TOPIC command is sent
func IrcTopicHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) < 1 {
		// ERR_NEEDMOREPARAMS
		if err := SendIrcNumeric(ctx, 461, ctx.Nick(), "TOPIC :Not enough parameters"); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		return
	}
	channame := args[0]
	topic := trailing
	channel, ok := ctx.Channels[channame]
	if !ok {
		log.Warningf("IrcTopicHandler: unknown channel %s", channame)
		return
	}
	newTopic, err := ctx.SlackClient.SetPurposeOfConversation(channel.ID, topic)
	if err != nil {
		// ERR_UNKNOWNERROR
		if err := SendIrcNumeric(ctx, 400, ctx.Nick(), fmt.Sprintf("%s :Cannot set topic: %v", channame, err)); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		return
	}
	// RPL_TOPIC
	if err := SendIrcNumeric(ctx, 332, fmt.Sprintf("%s :%s", ctx.Nick(), channame), newTopic.Purpose.Value); err != nil {
		log.Warningf("Failed to send IRC message: %v", err)
	}
}
