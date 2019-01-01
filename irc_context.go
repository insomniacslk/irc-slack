package main

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/nlopes/slack"
)

// SlackPostMessage represents a message sent to slack api
type SlackPostMessage struct {
	Target string
	Text   string
}

// IrcContext holds the client context information
type IrcContext struct {
	Conn *net.TCPConn
	User *slack.User
	// TODO make RealName a function
	RealName          string
	SlackClient       *slack.Client
	SlackRTM          *slack.RTM
	SlackAPIKey       string
	SlackConnected    bool
	ServerName        string
	Channels          map[string]Channel
	ChanMutex         *sync.Mutex
	Users             []slack.User
	ChunkSize         int
	postMessage       chan SlackPostMessage
	conversationCache map[string]*slack.Channel
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

// GetUsers returns a list of users of the Slack team the context is connected
// to
func (ic *IrcContext) GetUsers(refresh bool) []slack.User {
	if refresh || ic.Users == nil || len(ic.Users) == 0 {
		users, err := ic.SlackClient.GetUsers()
		if err != nil {
			log.Printf("Failed to get users: %v", err)
			return nil
		}
		ic.Users = users
		log.Printf("Fetched %v users", len(users))
	}
	return ic.Users
}

// Start handles batching of messages to slack
func (ic *IrcContext) Start() {
	textBuffer := make(map[string]string)
	timer := time.NewTimer(time.Second)
	var message SlackPostMessage
	for {
		select {
		case message = <-ic.postMessage:
			log.Printf("Got new message %v", message)
			textBuffer[message.Target] += message.Text + "\n"
			timer.Reset(time.Second)
		case <-timer.C:
			for target, text := range textBuffer {
				opts := []slack.MsgOption{}
				opts = append(opts, slack.MsgOptionAsUser(true))
				opts = append(opts, slack.MsgOptionText(strings.TrimSpace(text), false))
				ic.SlackClient.PostMessage(target, opts...)
			}
			textBuffer = make(map[string]string)
		}
	}
}

// PostTextMessage batches all messages that should be posted to slack
func (ic *IrcContext) PostTextMessage(target string, text string) {
	ic.postMessage <- SlackPostMessage{
		Target: target,
		Text:   text,
	}
}

// GetUserInfo returns a slack.User instance from a given user ID, or nil if
// no user with that ID was found
func (ic *IrcContext) GetUserInfo(userID string) *slack.User {
	users := ic.GetUsers(false)
	if users == nil || len(users) == 0 {
		return nil
	}
	// XXX this may be slow, convert user list to map?
	for _, user := range users {
		if user.ID == userID {
			return &user
		}
	}
	return nil
}

// GetUserInfoByName returns a slack.User instance from a given user name, or
// nil if no user with that name was found
func (ic *IrcContext) GetUserInfoByName(username string) *slack.User {
	users := ic.GetUsers(false)
	if users == nil || len(users) == 0 {
		return nil
	}
	for _, user := range users {
		if user.Name == username {
			return &user
		}
	}
	return nil
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

// UserIDsToNames returns a list of user names corresponding to a list of user
// IDs. If an ID is unknown, it is returned unmodified in the output list
func (ic IrcContext) UserIDsToNames(userIDs ...string) []string {
	var names []string
	// TODO implement using ic.GetUsers() instead
	allUsers := ic.GetUsers(true)
	usersMap := make(map[string]slack.User, len(allUsers))
	for _, user := range allUsers {
		usersMap[user.ID] = user
	}
	for _, uid := range userIDs {
		user, ok := usersMap[uid]
		if !ok {
			names = append(names, uid)
			log.Printf("Could not fetch user %s, not in user map", uid)
		} else {
			names = append(names, user.Name)
			log.Printf("Fetched info for user ID %s: %s", uid, user.Name)
		}
	}
	return names
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
