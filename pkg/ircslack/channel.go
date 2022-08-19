package ircslack

import (
	"fmt"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// Constants for public, private, and multi-party conversation prefixes.
// Channel threads are prefixed with "+" but they are not conversation types
// so they do not belong here. A thread is just a message whose destination
// is within another message in a public, private, or multi-party conversation.
const (
	ChannelPrefixPublicChannel  = "#"
	ChannelPrefixPrivateChannel = "@"
	ChannelPrefixMpIM           = "&"
	// NOTE: a thread is not a channel type
	ChannelPrefixThread = "+"
)

// HasChannelPrefix returns true if the channel name starts with one of the
// supproted channel prefixes.
func HasChannelPrefix(name string) bool {
	if len(name) == 0 {
		return false
	}
	switch string(name[0]) {
	case ChannelPrefixPublicChannel, ChannelPrefixPrivateChannel, ChannelPrefixMpIM, ChannelPrefixThread:
		return true
	default:
		return false
	}
}

// StripChannelPrefix returns a channel name without its channel prefix. If no
// channel prefix is present, the string is returned unchanged.
func StripChannelPrefix(name string) string {
	if HasChannelPrefix(name) {
		return name[1:]
	}
	return name
}

// ChannelMembers returns a list of users in the given conversation.
func ChannelMembers(ctx *IrcContext, channelID string) ([]slack.User, error) {
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
				return nil, fmt.Errorf("ChannelMembers: exceeded the maximum number of attempts (%d) with the Slack API", MaxSlackAPIAttempts)
			}
			log.Debugf("ChannelMembers: page %d attempt #%d nextCursor=%s", page, attempt, nextCursor)
			m, nextCursor, err = ctx.SlackClient.GetUsersInConversation(&slack.GetUsersInConversationParameters{ChannelID: channelID, Cursor: nextCursor, Limit: 1000})
			if err != nil {
				log.Errorf("Failed to get users in conversation '%s': %v", channelID, err)
				if rlErr, ok := err.(*slack.RateLimitedError); ok {
					// we were rate-limited. Let's wait as much as Slack
					// instructs us to do
					log.Warningf("Hit Slack API rate limiter. Waiting %v", rlErr.RetryAfter)
					time.Sleep(rlErr.RetryAfter)
					attempt++
					continue
				}
				return nil, fmt.Errorf("Cannot get member list for conversation %s: %v", channelID, err)
			}
			break
		}
		members = append(members, m...)
		log.Debugf("Fetched %d user IDs for channel %s (fetched so far: %d)", len(m), channelID, len(members))
		// TODO call ctx.Users.FetchByID here in a goroutine to see if this
		// speeds up
		if nextCursor == "" {
			break
		}
		page++
	}
	log.Debugf("Retrieving user information for %d users", len(members))
	users, err := ctx.Users.FetchByIDs(ctx.SlackClient, false, members...)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch users by their IDs: %v", err)
	}
	return users, nil
}

// Channel wraps a Slack conversation with a few utility functions.
type Channel slack.Channel

// IsPublicChannel returns true if the channel is public.
func (c *Channel) IsPublicChannel() bool {
	return c.IsChannel && !c.IsPrivate
}

// IsPrivateChannel returns true if the channel is private.
func (c *Channel) IsPrivateChannel() bool {
	return (c.IsGroup||c.IsChannel) && c.IsPrivate
}

// IsMP returns true if it is a multi-party conversation.
func (c *Channel) IsMP() bool {
	return c.IsMpIM
}

// IRCName returns the channel name as it would appear on IRC.
// Examples:
// * #channel for public groups
// * @channel for private groups
// * &Gxxxx|nick1-nick2-nick3 for multi-party IMs
func (c *Channel) IRCName() string {
	switch {
	case c.IsPublicChannel():
		return ChannelPrefixPublicChannel + c.Name
	case c.IsPrivateChannel():
		return ChannelPrefixPrivateChannel + c.Name
	case c.IsMP():
		name := ChannelPrefixMpIM + c.ID + "|" + c.Name
		name = strings.Replace(name, "mpdm-", "", -1)
		name = strings.Replace(name, "--", "-", -1)
		if len(name) >= 30 {
			return name[:29] + "…"
		}
		return name
	default:
		log.Warningf("Unknown channel type for channel %+v", c)
		return "<unknow-channel-type>"
	}
}

// SlackName returns the slack.Channel.Name field.
func (c *Channel) SlackName() string {
	return c.Name
}
