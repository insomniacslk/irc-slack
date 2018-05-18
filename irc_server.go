package main

import (
	"fmt"
	"html"
	"log"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/nlopes/slack"
)

// Project constants
const (
	ProjectAuthor      = "Andrea Barberio"
	ProjectAuthorEmail = "insomniac@slackware.it"
	ProjectURL         = "https://github.com/insomniacslk/irc-slack"
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
    "JOIN": IrcJoinHandler,
}

var (
	rxSlackUrls = regexp.MustCompile("<[^>]+>?")
	rxSlackUser = regexp.MustCompile("<@[UW][A-Z0-9]+>")
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
	reply := fmt.Sprintf(":%s %03d %s :%s\r\n", ctx.ServerName, code, args, desc)
	log.Printf("Sending numeric reply: %s", reply)
	_, err := ctx.Conn.Write([]byte(reply))
	return err
}

// IrcSendChanInfoAfterJoin sends channel information to the user about a joined
// channel.
func IrcSendChanInfoAfterJoin(ctx *IrcContext, name, topic string, members []string, isGroup bool) {
	// TODO wrap all these Conn.Write into a function
	ctx.Conn.Write([]byte(fmt.Sprintf(":%v JOIN #%v\r\n", ctx.Mask(), name)))
	// RPL_TOPIC
	SendIrcNumeric(ctx, 332, fmt.Sprintf("%s #%s", ctx.Nick, name), topic)
	// RPL_NAMREPLY
	SendIrcNumeric(ctx, 353, fmt.Sprintf("%s = #%s", ctx.Nick, name), strings.Join(ctx.UserIDsToNames(members...), " "))
	// RPL_ENDOFNAMES
	SendIrcNumeric(ctx, 366, fmt.Sprintf("%s #%s", ctx.Nick, name), "End of NAMES list")
	ctx.ChanMutex.Lock()
	ctx.Channels[name] = Channel{Topic: topic, Members: members, IsGroup: isGroup}
	ctx.ChanMutex.Unlock()
}

// IrcAfterLoggingIn is called once the user has successfully logged on IRC
func IrcAfterLoggingIn(ctx *IrcContext, rtm *slack.RTM) error {
	// Send a welcome to the user, to let the client knows that it's connected
	// RPL_WELCOME
	SendIrcNumeric(ctx, 1, ctx.Nick, fmt.Sprintf("Welcome to the %s IRC chat, %s!", ctx.ServerName, ctx.Nick))
	// RPL_MOTDSTART
	SendIrcNumeric(ctx, 375, ctx.Nick, "")
	// RPL_MOTD
	SendIrcNumeric(ctx, 372, ctx.Nick, fmt.Sprintf("This is an IRC-to-Slack gateway, written by %s <%s>.", ProjectAuthor, ProjectAuthorEmail))
	SendIrcNumeric(ctx, 372, ctx.Nick, fmt.Sprintf("More information at %s.", ProjectURL))
	// RPL_ENDOFMOTD
	SendIrcNumeric(ctx, 376, ctx.Nick, "")

	ctx.Channels = make(map[string]Channel)
	ctx.ChanMutex = &sync.Mutex{}

	// asynchronously get groups
	groupsErr := make(chan error, 1)
	go func(errors chan<- error) {
		groups, err := ctx.SlackClient.GetGroups(true)
		if err != nil {
			errors <- fmt.Errorf("Error getting Slack groups: %v", err)
			return
		}
		log.Print("Group list:")
		for _, g := range groups {
			log.Printf("--> #%s topic=%s", g.Name, g.Topic.Value)
			go IrcSendChanInfoAfterJoin(ctx, g.Name, g.Topic.Value, g.Members, true)
		}
		errors <- nil
	}(groupsErr)

	// asynchronously get channels
	chansErr := make(chan error, 1)
	go func(errors chan<- error) {
		log.Print("Channel list:")
		channels, err := ctx.SlackClient.GetChannels(true)
		if err != nil {
			errors <- fmt.Errorf("Error getting Slack channels: %v", err)
			return
		}
		for _, ch := range channels {
			var info string
			if ch.IsMember {
				var (
					members, m []string
					nextCursor string
					err        error
				)
				for {
					m, nextCursor, err = ctx.SlackClient.GetUsersInConversation(&slack.GetUsersInConversationParameters{ChannelID: ch.ID, Cursor: nextCursor})
					if err != nil {
						log.Printf("Cannot get member list for channel %s: %v", ch.Name, err)
						break
					}
					members = append(members, m...)
					log.Printf(" nextCursor=%v", nextCursor)
					if nextCursor == "" {
						break
					}
				}
				info = "(joined) "
				info += fmt.Sprintf(" topic=%s members=%d", ch.Topic.Value, len(members))
				// the channels are already joined, notify the IRC client of their
				// existence
				go IrcSendChanInfoAfterJoin(ctx, ch.Name, ch.Topic.Value, members, false)
			}
			log.Printf("  #%v %v", ch.Name, info)
		}
		errors <- nil
	}(chansErr)

	if err := <-groupsErr; err != nil {
		return err
	}
	if err := <-chansErr; err != nil {
		return err
	}

	go func(rtm *slack.RTM) {
		log.Print("Started Slack event listener")
		for msg := range rtm.IncomingEvents {
			switch ev := msg.Data.(type) {
			case *slack.MessageEvent:
				// get user
				var name string
				user := ctx.GetUserInfo(ev.Msg.User)
				if user == nil {
					log.Printf("Error getting user info for %v", ev.Msg.User)
					name = ev.Msg.User
				} else {
					name = user.Name
				}
				// get channel or other recipient (e.g. recipient of a direct message)
				var channame string
				if strings.HasPrefix(ev.Msg.Channel, "C") {
					// Channel message
					// TODO cache channel info
					channel, err := ctx.SlackClient.GetChannelInfo(ev.Msg.Channel)
					if err != nil {
						log.Printf("Error getting channel info for %v: %v", ev.Msg.Channel, err)
						channame = "unknown"
					} else {
						channame = "#" + channel.Name
					}
				} else if strings.HasPrefix(ev.Msg.Channel, "D") {
					// Direct message to me
					channame = ctx.Nick
				} else {
					log.Printf("Unknown recipient ID: %s", ev.Msg.Channel)
					return
				}

				log.Printf("SLACK msg from %v (%v) on %v: %v",
					ev.Msg.User,
					name,
					ev.Msg.Channel,
					ev.Msg.Text,
				)
				if ev.Msg.User == "" && ev.Msg.Text == "" {
					log.Printf("WARNING: empty user and message: %+v", ev.Msg)
					continue
				}
				// replace UIDs with user names
				text := ev.Msg.Text
				// replace UIDs with nicknames
				text = rxSlackUser.ReplaceAllStringFunc(text, func(subs string) string {
					uid := subs[2 : len(subs)-1]
					user := ctx.GetUserInfo(uid)
					if user == nil {
						return subs
					}
					return fmt.Sprintf("@%s", user.Name)
				})
				// replace some HTML entities
				text = ExpandText(text)

				// FIXME if two instances are connected to the Slack API at the
				// same time, this will hide the other instance's message
				// believing it was sent from here. But since it's not, both
				// local echo and remote message won't be shown
				botID := msg.Data.(*slack.MessageEvent).BotID
				if name == ctx.Nick && botID != user.Profile.BotID {
					// don't print my own messages
					continue
				}
				// handle multi-line messages
				for _, line := range strings.Split(text, "\n") {
					privmsg := fmt.Sprintf(":%v!%v@%v PRIVMSG %v :%v\r\n",
						name, ev.Msg.User, ctx.ServerName,
						channame, line,
					)
					log.Print(privmsg)
					ctx.Conn.Write([]byte(privmsg))
				}
				msgEv := msg.Data.(*slack.MessageEvent)
				// Check if the topic has changed
				if msgEv.Topic != ctx.Channels[msgEv.Channel].Topic {
					// Send out new topic
					channel, err := ctx.SlackClient.GetChannelInfo(msgEv.Channel)
					if err != nil {
						log.Printf("Cannot get channel name for %v", msgEv.Channel)
					} else {
						newTopic := fmt.Sprintf(":%v TOPIC #%v :%v\r\n", ctx.Mask(), channel.Name, msgEv.Topic)
						log.Printf("Got new topic: %v", newTopic)
						ctx.Conn.Write([]byte(newTopic))
					}
				}
				// check if new people joined the channel
				added, removed := ctx.Channels[msgEv.Channel].MembersDiff(msgEv.Members)
				if len(added) > 0 || len(removed) > 0 {
					log.Printf("[*] People who joined: %v", added)
					log.Printf("[*] People who left: %v", removed)
				}
			case *slack.ConnectedEvent:
				log.Print("Connected to Slack")
				ctx.SlackConnected = true
			case *slack.DisconnectedEvent:
				log.Printf("Disconnected from Slack (intentional: %v)", msg.Data.(*slack.DisconnectedEvent).Intentional)
				ctx.SlackConnected = false
				ctx.Conn.Close()
			case *slack.MemberJoinedChannelEvent, *slack.MemberLeftChannelEvent:
				// refresh the users list
				// FIXME also send a JOIN / PART message to the IRC client
				ctx.GetUsers(true)
			default:
				log.Printf("SLACK event: %v: %v", msg.Type, msg.Data)
			}
		}
	}(rtm)
	return nil
}

// IrcCapHandler is called when a CAP command is sent
func IrcCapHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) > 1 {
		if args[0] == "LS" {
			reply := fmt.Sprintf(":%s CAP * LS :\r\n", ctx.ServerName)
			ctx.Conn.Write([]byte(reply))
		} else {
			log.Printf("Got CAP %v", args)
		}
	}
}

// IrcPrivMsgHandler is called when a PRIVMSG command is sent
func IrcPrivMsgHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) != 1 {
		log.Printf("Invalid PRIVMSG command args: %v", args)
	}
	target := args[0]
	if !strings.HasPrefix(target, "#") {
		// Send to user instead of channel
		target = "@" + target
	}
	text := trailing
	params := slack.NewPostMessageParameters()
	params.AsUser = true
	ctx.SlackClient.PostMessage(target, text, params)
}

// IrcNickHandler is called when a NICK command is sent
func IrcNickHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) < 1 {
		log.Printf("Invalid NICK command args: %v", args)
	}
	nick := args[0]
	// No need to handle nickname collisions, there can be multiple instances
	// of the same user connected at the same time
	/*
		if _, ok := UserNicknames[nick]; ok {
			log.Printf("Nickname %v already in use", nick)
			// ERR_NICKNAMEINUSE
			SendIrcNumeric(ctx, 433, fmt.Sprintf("* %s", nick), fmt.Sprintf("Nickname %s already in use", nick))
			return
		}
	*/
	UserNicknames[nick] = ctx
	log.Printf("Setting nickname for %v to %v", ctx.Conn.RemoteAddr(), nick)
	ctx.Nick = nick
	if ctx.SlackClient == nil {
		ctx.SlackClient = slack.New(ctx.SlackAPIKey)
		logger := log.New(os.Stdout, "slack: ", log.Lshortfile|log.LstdFlags)
		slack.SetLogger(logger)
		ctx.SlackClient.SetDebug(false)
		rtm := ctx.SlackClient.NewRTM()
		go rtm.ManageConnection()
		log.Print("Started Slack client")
		if err := IrcAfterLoggingIn(ctx, rtm); err != nil {
			log.Print(err)
		}
	}
}

// IrcUserHandler is called when a USER command is sent
func IrcUserHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if ctx.Nick == "" {
		log.Print("Empty nickname!")
		return
	}
	if len(args) < 3 {
		log.Printf("Invalid USER command args: %s", args)
	}
	log.Printf("Contexts: %v", UserContexts)
	log.Printf("Nicknames: %v", UserNicknames)
	// TODO implement `mode` as per https://tools.ietf.org/html/rfc2812#section-3.1.3
	username, _, _ := args[0], args[1], args[2]
	ctx.UserName = username
	ctx.RealName = trailing
}

// IrcPingHandler is called when a PING command is sent
func IrcPingHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	msg := fmt.Sprintf("PONG %s", strings.Join(args, " "))
	if trailing != "" {
		msg += " :" + trailing
	}
	ctx.Conn.Write([]byte(msg + "\r\n"))
}

// IrcQuitHandler is called when a QUIT command is sent
func IrcQuitHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	ctx.Conn.Close()
}

// IrcModeHandler is called when a MODE command is sent
func IrcModeHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) == 1 {
		// get mode request. Always no mode (for now)
		mode := "+"
		// RPL_CHANNELMODEIS
		SendIrcNumeric(ctx, 324, fmt.Sprintf("%s %s %s", ctx.Nick, args[0], mode), "")
	} else if len(args) > 1 {
		// set mode request. Not handled yet
		// TODO handle mode set
		// ERR_UMODEUNKNOWNFLAG
		SendIrcNumeric(ctx, 501, args[0], fmt.Sprintf("Unknown MODE flags %s", strings.Join(args[1:], " ")))
	} else {
		// TODO send an error
	}
}

// IrcPassHandler is called when a PASS command is sent
func IrcPassHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) != 1 {
		log.Printf("Invalid PASS arguments: %s", args)
		// ERR_PASSWDMISMATCH
		SendIrcNumeric(ctx, 464, "", "Invalid password")
		return
	}
	ctx.SlackAPIKey = args[0]
}

// IrcWhoisHandler is called when a WHOIS command is sent
func IrcWhoisHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) != 1 && len(args) != 2 {
		// ERR_UNKNOWNERROR
		SendIrcNumeric(ctx, 400, ctx.Nick, "Invalid WHOIS command")
		return
	}
	username := args[0]
	// if the second argument is the same as the first, it's a request of WHOIS
	// with idle time
	// TODO handle idle time, args[1]
	user := ctx.GetUserInfoByName(username)
	if user == nil {
		// ERR_NOSUCHNICK
		SendIrcNumeric(ctx, 401, ctx.Nick, fmt.Sprintf("No such nick %s", username))
	} else {
		// RPL_WHOISUSER
		SendIrcNumeric(ctx, 311, fmt.Sprintf("%s %s %s %s *", username, user.Name, user.ID, "localhost"), user.RealName)
		// RPL_WHOISSERVER
		SendIrcNumeric(ctx, 312, fmt.Sprintf("%s %s", username, ctx.ServerName), ctx.ServerName)
		// RPL_ENDOFWHOIS
		SendIrcNumeric(ctx, 319, ctx.Nick, username)
	}
}

// IrcWhoisHandler is called when a JOIN command is sent
func IrcJoinHandler(ctx *IrcContext, prefix, cmd string, args []string, trailing string) {
	if len(args) != 1 {
		// ERR_UNKNOWNERROR
		SendIrcNumeric(ctx, 400, ctx.Nick, "Invalid JOIN command")
		return
	}
	channame := args[0]
	ch, err := ctx.SlackClient.JoinChannel(channame)
	if err != nil {
		log.Printf("Cannot join channel %s: %v", channame, err)
		return
	}
	log.Printf("Joined channel %s", ch)
	go IrcSendChanInfoAfterJoin(ctx, ch.Name, ch.Topic.Value, ch.Members, true)
}
