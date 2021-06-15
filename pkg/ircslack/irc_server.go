package ircslack

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
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
	"NAMES":   IrcNamesHandler,
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
func IrcSendChanInfoAfterJoin(ctx *IrcContext, ch *Channel, members []slack.User) {
	IrcSendChanInfoAfterJoinCustom(ctx, ch.IRCName(), ch.ID, ch.Purpose.Value, members)
}

// IrcSendChanInfoAfterJoinCustom sends channel information to the user about a joined
// channel. It can be used as an alternative to IrcSendChanInfoAfterJoin when
// you need to specify custom chan name, id, and topic.
func IrcSendChanInfoAfterJoinCustom(ctx *IrcContext, chanName, chanID, topic string, members []slack.User) {
	memberNames := make([]string, 0, len(members))
	for _, m := range members {
		memberNames = append(memberNames, m.Name)
	}
	// TODO wrap all these Conn.Write into a function
	if _, err := ctx.Conn.Write([]byte(fmt.Sprintf(":%s JOIN %s\r\n", ctx.Mask(), chanName))); err != nil {
		log.Warningf("Failed to send IRC JOIN message: %v", err)
	}
	// RPL_TOPIC
	if err := SendIrcNumeric(ctx, 332, fmt.Sprintf("%s %s", ctx.Nick(), chanName), topic); err != nil {
		log.Warningf("Failed to send IRC TOPIC message: %v", err)
	}
	// RPL_NAMREPLY
	if len(members) > 0 {
		if err := SendIrcNumeric(ctx, 353, fmt.Sprintf("%s = %s", ctx.Nick(), chanName), strings.Join(memberNames, " ")); err != nil {
			log.Warningf("Failed to send IRC NAMREPLY message: %v", err)
		}
	}
	// RPL_ENDOFNAMES
	if err := SendIrcNumeric(ctx, 366, fmt.Sprintf("%s %s", ctx.Nick(), chanName), "End of NAMES list"); err != nil {
		log.Warningf("Failed to send IRC ENDOFNAMES message: %v", err)
	}
	log.Infof("Joined channel %s", chanName)
}

// joinChannel will join the channel with the given ID, name and topic, and send back a
// response to the IRC client
func joinChannel(ctx *IrcContext, ch *Channel) error {
	log.Infof(fmt.Sprintf("%s topic=%s members=%d", ch.IRCName(), ch.Purpose.Value, ch.NumMembers))
	// the channels are already joined, notify the IRC client of their
	// existence
	members, err := ChannelMembers(ctx, ch.ID)
	if err != nil {
		jErr := fmt.Errorf("Failed to fetch users in channel `%s (channel ID: %s): %v", ch.Name, ch.ID, err)
		ctx.SendUnknownError(jErr.Error())
		return jErr
	}
	go IrcSendChanInfoAfterJoin(ctx, ch, members)
	return nil
}

// joinChannels gets all the available Slack channels and sends an IRC JOIN message
// for each of the joined channels on Slack
func joinChannels(ctx *IrcContext) error {
	for _, sch := range ctx.Channels.AsMap() {
		ch := Channel(sch)
		if !ch.IsPublicChannel() && !ch.IsPrivateChannel() {
			continue
		}
		if ch.IsMember {
			if err := joinChannel(ctx, &ch); err != nil {
				return err
			}
		}
	}
	return nil
}

// IrcAfterLoggingIn is called once the user has successfully logged on IRC
func IrcAfterLoggingIn(ctx *IrcContext, rtm *slack.RTM) error {
	if ctx.OrigName != ctx.Nick() {
		// Force the user into the Slack nick
		if _, err := ctx.Conn.Write([]byte(fmt.Sprintf(":%s NICK %s\r\n", ctx.OrigName, ctx.Nick()))); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
	}
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
	// RPL_ISUPPORT
	if err := SendIrcNumeric(ctx, 005, ctx.Nick(), "CHANTYPES="+strings.Join(SupportedChannelPrefixes(), "")); err != nil {
		log.Warningf("Failed to send IRC message: %v", err)
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
	var channelParameter, text string
	switch len(args) {
	case 1:
		channelParameter = args[0]
		text = trailing
	case 2:
		channelParameter = args[0]
		text = args[1]
	default:
		log.Warningf("Invalid number of parameters for PRIVMSG, want 1 or 2, got %d", len(args))
	}
	if channelParameter == "" || text == "" {
		log.Warningf("Invalid PRIVMSG command args: %v %v", args, trailing)
		return
	}
	channel := ctx.Channels.ByName(channelParameter)
	target := ""
	if channel != nil {
		// known channel
		target = channel.SlackName()
	} else {
		// assume private message
		target = "@" + channelParameter
	}

	if strings.HasPrefix(text, "\x01ACTION ") && strings.HasSuffix(text, "\x01") {
		// The Slack API has a bug, where a chat.meMessage is
		// documented to accept a channel name or ID, but actually
		// only the channel ID will work. So until this is fixed,
		// resolve the channel ID for chat.meMessage .
		// TODO revert this when the bug in the Slack API is fixed
		key := target
		ch := ctx.Channels.ByName(key)
		if ch == nil {
			log.Warningf("Unknown channel ID for %s", key)
			return
		}
		target = ch.SlackName()

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
	if cookie == "" {
		// legacy token
		ctx.usingLegacyToken = true
	}
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
	// the users cache is not yet populated at this point, so we call the Slack
	// API directly.
	user, err := ctx.SlackClient.GetUserInfo(info.User.ID)
	if err != nil {
		return fmt.Errorf("Cannot get info for user %s (ID: %s): %v", info.User.Name, info.User.ID, err)
	}
	ctx.User = user
	ctx.RealName = user.RealName
	// do not fetch users here, they will be fetched later upon joining channels
	if err := ctx.Channels.Fetch(ctx.SlackClient); err != nil {
		ctx.Conn.Close()
		return fmt.Errorf("Failed to fetch channels: %v", err)
	}
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

	if ctx.SlackClient != nil {
		if nick != ctx.Nick() {
			// You cannot change nick, so force it back
			if _, err := ctx.Conn.Write([]byte(fmt.Sprintf(":%s NICK %s\r\n", nick, ctx.Nick()))); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
		}
		return
	}

	// We need the original nick later to change it
	ctx.OrigName = nick

	// If we're ready, connect
	if ctx.RealName != "" && ctx.SlackAPIKey != "" {
		if err := connectToSlack(ctx); err != nil {
			log.Warningf("Cannot connect to Slack: %v", err)
			// close the IRC connection to the client
			ctx.Conn.Close()
		}
	}
}

// IrcUserHandler is called when a USER command is sent
func IrcUserHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	// ignore the user-specified username. Will use the Slack ID instead
	// TODO get user info and set the real name with that info
	ctx.RealName = trailing

	// If we're ready, connect
	if ctx.SlackClient == nil && ctx.SlackAPIKey != "" && ctx.OrigName != "" {
		if err := connectToSlack(ctx); err != nil {
			log.Warningf("Cannot connect to Slack: %v", err)
			// close the IRC connection to the client
			ctx.Conn.Close()
		}
	}
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

	// If we're ready, connect
	if ctx.SlackClient == nil && ctx.RealName != "" && ctx.OrigName != "" {
		if err := connectToSlack(ctx); err != nil {
			log.Warningf("Cannot connect to Slack: %v", err)
			// close the IRC connection to the client
			ctx.Conn.Close()
		}
	}
}

// IrcWhoHandler is called when a WHO command is sent
func IrcWhoHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	sendErr := func() {
		ctx.SendUnknownError("Invalid WHO command. Syntax: WHO <nickname|channel>")
	}
	if len(args) != 1 && len(args) != 2 {
		sendErr()
		return
	}
	target := args[0]
	var rargs, desc string
	if HasChannelPrefix(target) {
		ch := ctx.Channels.ByName(target)
		if ch == nil {
			// ERR_NOSUCHCHANNEL
			if err := SendIrcNumeric(ctx, 403, ctx.Nick(), fmt.Sprintf("No such channel %s", target)); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
			return
		}
		for _, un := range ch.Members {
			// FIXME can we use the cached users?
			u := ctx.Users.ByID(un)
			if u == nil {
				log.Warningf("Failed to get info for user name '%s'", un)
				continue
			}
			log.Infof("%+v", u.Name)
			rargs = fmt.Sprintf("%s %s %s %s %s %s *", ctx.Nick(), target, u.ID, ctx.ServerName, ctx.ServerName, u.Name)
			desc = fmt.Sprintf("0 %s", u.RealName)
			// RPL_WHOREPLY
			// "<channel> <user> <host> <server> <nick> \
			//  <H|G>[*][@|+] :<hopcount> <real name>"
			if err := SendIrcNumeric(ctx, 352, rargs, desc); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
		}
		// RPL_ENDOFWHO
		// "<name> :End of /WHO list"
		if err := SendIrcNumeric(ctx, 315, fmt.Sprintf("%s %s", ctx.Nick(), target), "End of WHO list"); err != nil {
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
	// FIXME get channel
	rargs = fmt.Sprintf("#general %s %s %s %s %s *", ctx.Nick(), user.ID, ctx.ServerName, ctx.ServerName, user.Name)
	desc = fmt.Sprintf("0 %s", user.RealName)
	// RPL_WHOREPLY
	// "<channel> <user> <host> <server> <nick> \
	//  <H|G>[*][@|+] :<hopcount> <real name>"
	if err := SendIrcNumeric(ctx, 352, rargs, desc); err != nil {
		log.Warningf("Failed to send IRC message: %v", err)
	}
}

// IrcWhoisHandler is called when a WHOIS command is sent
func IrcWhoisHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) != 1 && len(args) != 2 {
		ctx.SendUnknownError("Invalid WHOIS command. Syntax: WHOIS <username>")
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
		// "<nick> <user> <host> * :<real name>"
		if err := SendIrcNumeric(ctx, 311, fmt.Sprintf("%s %s %s %s *", ctx.Nick(), username, user.ID, ctx.ServerName), user.RealName); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		// RPL_WHOISSERVER
		// "<nick> <server> :<server info>"
		if err := SendIrcNumeric(ctx, 312, fmt.Sprintf("%s %s %s", ctx.Nick(), username, ctx.ServerName), "irc-slack, https://github.com/insomniacslk/irc-slack"); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		// Send additional user status information, abusing the RPL_WHOISSERVER
		// reply. If there is a better method, please let us know!
		if user.Profile.StatusText != "" || user.Profile.StatusEmoji != "" {
			userStatus := fmt.Sprintf("user status: '%s' %s", user.Profile.StatusText, user.Profile.StatusEmoji)
			if user.Profile.StatusExpiration != 0 {
				userStatus += " until " + time.Unix(int64(user.Profile.StatusExpiration), 0).String()
			}
			if err := SendIrcNumeric(ctx, 312, fmt.Sprintf("%s %s %s", ctx.Nick(), username, ctx.ServerName), userStatus); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
		}
		// RPL_WHOISCHANNELS
		// "<nick> :{[@|+]<channel><space>}"
		var channels []string
		for chname, ch := range ctx.Channels.AsMap() {
			for _, u := range ch.Members {
				if u == user.ID {
					channels = append(channels, chname)
				}
			}
		}
		if err := SendIrcNumeric(ctx, 319, fmt.Sprintf("%s %s", ctx.Nick(), username), strings.Join(channels, " ")); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		if withIdleTime {
			// TODO send RPL_WHOISIDLE (317)
			// "<nick> <integer> :seconds idle"
		}
		// RPL_ENDOFWHOIS
		// "<nick> :End of /WHOIS list"
		if err := SendIrcNumeric(ctx, 318, fmt.Sprintf("%s %s", ctx.Nick(), username), ":End of /WHOIS list"); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
	}
}

// IrcJoinHandler is called when a JOIN command is sent
func IrcJoinHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) != 1 {
		ctx.SendUnknownError("Invalid JOIN command")
		return
	}
	// Because it is possible for an IRC Client to join multiple channels
	// via a multi join (e.g. /join #chan1,#chan2,#chan3) the argument
	// needs to be splitted by commas and each channel needs to be joined
	// separately.
	channames := strings.Split(args[0], ",")
	for _, channame := range channames {
		if strings.HasPrefix(channame, ChannelPrefixMpIM) || strings.HasPrefix(channame, ChannelPrefixThread) {
			log.Debugf("JOIN: ignoring channel `%s`, cannot join multi-party IMs or threads", channame)
			continue
		}
		sch, _, _, err := ctx.SlackClient.JoinConversation(channame)
		if err != nil {
			log.Warningf("Cannot join channel %s: %v", channame, err)
			continue
		}
		log.Infof("Joined channel %s", channame)
		ch := Channel(*sch)
		if err := joinChannel(ctx, &ch); err != nil {
			log.Warningf("Failed to join channel `%s`: %v", ch.Name, err)
			continue
		}
	}
}

// IrcPartHandler is called when a PART command is sent
func IrcPartHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) != 1 {
		ctx.SendUnknownError("Invalid PART command")
		return
	}
	channame := StripChannelPrefix(args[0])
	// Slack needs the channel ID to leave it, not the channel name. The only
	// way to get the channel ID from the name is retrieving the whole channel
	// list and finding the one whose name is the one we want to leave
	if err := ctx.Channels.Fetch(ctx.SlackClient); err != nil {
		log.Warningf("Cannot leave channel %s: %v", channame, err)
		ctx.SendUnknownError("Cannot leave channel: %v", err)
		return
	}
	var chanID string
	for _, ch := range ctx.Channels.AsMap() {
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
		notInChan, err := ctx.SlackClient.LeaveConversation(chanID)
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
	channel := ctx.Channels.ByName(channame)
	if channel == nil {
		log.Warningf("IrcTopicHandler: unknown channel %s", channame)
		return
	}
	newTopic, err := ctx.SlackClient.SetPurposeOfConversation(channel.ID, topic)
	if err != nil {
		ctx.SendUnknownError("%s :Cannot set topic: %v", channame, err)
		return
	}
	// RPL_TOPIC
	if err := SendIrcNumeric(ctx, 332, fmt.Sprintf("%s :%s", ctx.Nick(), channame), newTopic.Purpose.Value); err != nil {
		log.Warningf("Failed to send IRC message: %v", err)
	}
}

// IrcNamesHandler is called when a NAMES command is sent
func IrcNamesHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) < 1 {
		// ERR_NEEDMOREPARAMS
		if err := SendIrcNumeric(ctx, 461, ctx.Nick(), "NAMES :Not enough parameters"); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
		return
	}
	ch := ctx.Channels.ByName(args[0])
	if ch == nil {
		ctx.SendUnknownError("Channel `%s` not found", args[0])
		return
	}

	members, err := ChannelMembers(ctx, ch.ID)
	if err != nil {
		jErr := fmt.Errorf("Failed to fetch users in channel `%s (channel ID: %s): %v", ch.Name, ch.ID, err)
		ctx.SendUnknownError(jErr.Error())
		return
	}
	memberNames := make([]string, 0, len(members))
	for _, m := range members {
		memberNames = append(memberNames, m.Name)
	}
	log.Printf("Found %d members in %s: %v", len(memberNames), ch.IRCName(), memberNames)
	// RPL_NAMREPLY
	if len(members) > 0 {
		if err := SendIrcNumeric(ctx, 353, fmt.Sprintf("%s = %s", ctx.Nick(), ch.IRCName()), strings.Join(memberNames, " ")); err != nil {
			log.Warningf("Failed to send IRC NAMREPLY message: %v", err)
		}
	}
	// RPL_ENDOFNAMES
	if err := SendIrcNumeric(ctx, 366, fmt.Sprintf("%s %s", ctx.Nick(), ch.IRCName()), "End of NAMES list"); err != nil {
		log.Warningf("Failed to send IRC ENDOFNAMES message: %v", err)
	}
}
