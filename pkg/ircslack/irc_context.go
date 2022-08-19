package ircslack

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// SlackPostMessage represents a message sent to slack api
type SlackPostMessage struct {
	Target   string
	TargetTs string
	Text     string
}

// IrcContext holds the client context information
type IrcContext struct {
	Conn net.Conn
	User *slack.User
	// TODO make RealName a function
	RealName          string
	OrigName          string
	SlackClient       *slack.Client
	SlackRTM          *slack.RTM
	SlackAPIKey       string
	SlackDebug        bool
	SlackConnected    bool
	ServerName        string
	IsAuthenticating  bool
	AuthBase64        string
	Channels          *Channels
	Users             *Users
	ChunkSize         int
	postMessage       chan SlackPostMessage
	conversationCache map[string]*slack.Channel
	FileHandler       *FileHandler
	// set to `true` if we are using a deprecated legacy token, false otherwise
	usingLegacyToken bool
}

// Nick returns the nickname of the user, if known
func (ic *IrcContext) Nick() string {
	if ic.User == nil {
		return "<unknown>"
	}
	return ic.User.Name
}

// UserName returns the user's name. Currently this is equivalent to the user's
// Slack ID
func (ic *IrcContext) UserName() string {
	if ic.User == nil {
		return "<unknown>"
	}
	return ic.User.ID
}

// GetThreadOpener returns text of the first message in a thread that provided message belongs to
func (ic *IrcContext) GetThreadOpener(channel string, threadTimestamp string) (slack.Message, error) {
	msgs, _, _, err := ic.SlackClient.GetConversationReplies(&slack.GetConversationRepliesParameters{
		ChannelID: channel,
		Timestamp: threadTimestamp,
	})
	if err != nil || len(msgs) == 0 {
		return slack.Message{}, err
	}
	return msgs[0], nil
}

// ExpandUserIds will convert slack user tags with user's nicknames
func (ic *IrcContext) ExpandUserIds(text string) string {
	return rxSlackUser.ReplaceAllStringFunc(text, func(subs string) string {
		uid := subs[2 : len(subs)-1]
		user := ic.GetUserInfo(uid)
		if user == nil {
			return subs
		}
		return fmt.Sprintf("@%s", user.Name)
	})
}

// Start handles batching of messages to slack
func (ic *IrcContext) Start() {
	textBuffer := make(map[string]string)
	timer := time.NewTimer(time.Second)
	var message SlackPostMessage
	for {
		select {
		case message = <-ic.postMessage:
			log.Debugf("Got new message %v", message)
			textBuffer[message.Target] += message.Text + "\n"
			timer.Reset(time.Second)
		case <-timer.C:
			for target, text := range textBuffer {
				opts := []slack.MsgOption{}
				opts = append(opts, slack.MsgOptionAsUser(true))
				opts = append(opts, slack.MsgOptionText(strings.TrimSpace(text), false))
				if message.TargetTs != "" {
					opts = append(opts, slack.MsgOptionTS(message.TargetTs))
				}
				if _, _, err := ic.SlackClient.PostMessage(target, opts...); err != nil {
					log.Warningf("Failed to post message to Slack to target %s: %v", target, err)
				}
			}
			textBuffer = make(map[string]string)
		}
	}
}

// PostTextMessage batches all messages that should be posted to slack
func (ic *IrcContext) PostTextMessage(target, text, targetTs string) {
	ic.postMessage <- SlackPostMessage{
		Target:   target,
		TargetTs: targetTs,
		Text:     text,
	}
}

// GetUserInfo returns a slack.User instance from a given user ID, or nil if
// no user with that ID was found
func (ic *IrcContext) GetUserInfo(userID string) *slack.User {
	u := ic.Users.ByID(userID)
	if u == nil {
		log.Warningf("GetUserInfo: unknown user ID '%s'", userID)
	}
	return u
}

// GetUserInfoByName returns a slack.User instance from a given user name, or
// nil if no user with that name was found
func (ic *IrcContext) GetUserInfoByName(username string) *slack.User {
	u := ic.Users.ByName(username)
	if u == nil {
		log.Warningf("GetUserInfoByName: unknown user name '%s'", username)
	}
	return u
}

// UserID returns the user's Slack ID
func (ic IrcContext) UserID() string {
	if ic.User == nil {
		return "<unknown>"
	}
	return ic.User.ID
}

// Mask returns the IRC mask for the current user
func (ic IrcContext) Mask() string {
	return fmt.Sprintf("%v!%v@%v", ic.Nick(), ic.UserName(), ic.Conn.RemoteAddr().(*net.TCPAddr).IP)
}

// GetConversationInfo is cached version of slack.GetConversationInfo
func (ic IrcContext) GetConversationInfo(conversation string) (*slack.Channel, error) {
	c, ok := ic.conversationCache[conversation]
	if ok {
		return c, nil
	}
	c, err := ic.SlackClient.GetConversationInfo(conversation, false)
	if err != nil {
		return c, err
	}
	ic.conversationCache[conversation] = c
	return c, nil
}

// Maps of user contexts and nicknames
var (
	UserContexts = map[net.Addr]*IrcContext{}
)

// SendUnknownError sends an IRC 400 (ERR_UNKNOWNERROR) message to the client
// and prints a warning about it.
func (ic *IrcContext) SendUnknownError(fmtstr string, args ...interface{}) {
	msg := fmt.Sprintf(fmtstr, args...)
	log.Warningf("Sending ERR_UNKNOWNERROR (400) to client with message: %s", msg)
	if err := SendIrcNumeric(ic, 400, ic.Nick(), msg); err != nil {
		log.Warningf("Failed to send ERR_UNKNOWNERROR (400) to client: %v", err)
	}
}
