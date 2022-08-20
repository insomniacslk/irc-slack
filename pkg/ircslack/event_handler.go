package ircslack

import (
	"fmt"
	"math"
	"strings"

	"github.com/slack-go/slack"
)

func joinText(first string, second string, separator string) string {
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	return first + separator + second
}

func formatThreadChannelName(threadTimestamp string, channel *Channel) string {
	return ChannelPrefixThread + channel.Name + "-" + threadTimestamp
}

func resolveChannelName(ctx *IrcContext, msgChannel, threadTimestamp string) string {
	if strings.HasPrefix(msgChannel, "C") || strings.HasPrefix(msgChannel, "G") {
		// Channel message
		channel := ctx.Channels.ByID(msgChannel)
		if channel == nil {
			// try fetching it, in case it's a new channel
			channels, err := ctx.Channels.FetchByIDs(ctx.SlackClient, false, msgChannel)
			if err != nil || len(channels) == 0 {
				ctx.SendUnknownError("Failed to fetch channel with ID `%s`: %v", msgChannel, err)
				return ""
			}
			channel = &channels[0]
		}

		if channel == nil {
			ctx.SendUnknownError("Unknown channel ID `%s` when resolving channel name", msgChannel)
			return ""
		} else if threadTimestamp != "" {
			channame := formatThreadChannelName(threadTimestamp, channel)
			openingText, err := ctx.GetThreadOpener(msgChannel, threadTimestamp)
			if err != nil {
				ctx.SendUnknownError("Failed to get thread opener for `%s`: %v", msgChannel, err)
				return ""
			}
			IrcSendChanInfoAfterJoinCustom(
				ctx,
				channame,
				msgChannel,
				openingText.Text,
				[]slack.User{},
			)

			privmsg := fmt.Sprintf(":%v!%v@%v PRIVMSG %v :%s%s%s\r\n",
				channame, openingText.User, ctx.ServerName,
				channame, "", openingText.Text, "",
			)
			if _, err := ctx.Conn.Write([]byte(privmsg)); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
			return channame
		} else if channel.IsMpIM {
			if ctx.Channels.ByName(channel.IRCName()) == nil {
				members, err := ChannelMembers(ctx, channel.ID)
				if err != nil {
					log.Warningf("Failed to fetch channel members for `%s`: %v", channel.Name, err)
				} else {
					IrcSendChanInfoAfterJoin(ctx, channel, members)
				}
			}
			return channel.IRCName()
		}

		return channel.IRCName()
	} else if strings.HasPrefix(msgChannel, "D") {
		// Direct message to me
		channel := ctx.Channels.ByID(msgChannel)
		if channel == nil {
			// not found locally, try to get it via Slack API
			channels, err := ctx.Channels.FetchByIDs(ctx.SlackClient, false, msgChannel)
			if err != nil || len(channels) == 0 {
				ctx.SendUnknownError("Failed to fetch IM chat with ID `%s`: %v", msgChannel, err)
				return ""
			}
			channel = &channels[0]
		}
		members, err := ChannelMembers(ctx, channel.ID)
		if err != nil {
			ctx.SendUnknownError("Failed to fetch channel members for `%s`: %v", channel.Name, err)
			return ""
		}
		// we expect only two members in a direct message. Raise an
		// error if not.
		if len(members) == 0 || len(members) > 2 {
			ctx.SendUnknownError("Want 1 or 2 users in conversation, got %d (conversation ID: %s)", len(members), msgChannel)
			return ""
		}
		// of the two users, one is me. Otherwise fail
		if ctx.UserID() == "" {
			ctx.SendUnknownError("Cannot get my own user ID")
			return ""
		}
		user1 := members[0]
		var user2 slack.User
		if len(members) == 2 {
			user2 = members[1]
		} else {
			// len is 1. Sending a message to myself
			user2 = user1
		}
		if user1.ID != ctx.UserID() && user2.ID != ctx.UserID() {
			ctx.SendUnknownError("Got a direct message where I am not part of the members list (conversation: %s)", msgChannel)
			return ""
		}
		var recipientID string
		if user1.ID == ctx.UserID() {
			// then it's the other user
			recipientID = user2.ID
		} else {
			recipientID = user1.ID
		}
		// now resolve the ID to the user's nickname
		nickname := ctx.GetUserInfo(recipientID)
		if nickname == nil {
			// ERR_UNKNOWNERROR
			ctx.SendUnknownError("Unknown destination user ID %s for direct message %s", recipientID, msgChannel)
			return ""
		}
		return nickname.Name
	}
	log.Warningf("Unknown recipient ID: %s", msgChannel)
	return ""
}

func appendIfNotMoreThan(slice []slack.Msg, msg slack.Msg) []slack.Msg {
	if len(slice) == 100 {
		return append(slice[1:], msg)
	}
	return append(slice, msg)
}

func getConversationDetails(
	ctx *IrcContext,
	channelID string,
	timestamp string,
) (slack.Message, error, string) {
	message, err := ctx.SlackClient.GetConversationHistory(&slack.GetConversationHistoryParameters{
		ChannelID: channelID,
		Latest:    timestamp,
		Limit:     1,
		Inclusive: true,
	})
	if err != nil {
		return slack.Message{}, err, ""
	}
	if len(message.Messages) > 0 {
		parent := message.Messages[0]
		// If the timestamps are not equal, we're looking for a threaded message
		if parent.Timestamp != timestamp {
			msgs, _, _, err := ctx.SlackClient.GetConversationReplies(&slack.GetConversationRepliesParameters{ ChannelID: channelID, Timestamp: parent.Timestamp })
			if err == nil {
				for _, msg := range msgs {
					if msg.Timestamp == timestamp {
						channame := resolveChannelName(ctx, channelID, parent.Timestamp)
						return msg, nil, channame
					}
				}
			}
			// TODO: Always find the message, or return better fallback
			log.Warningf("Did not find threaded message with timestamp %v from %v", timestamp, parent);
		}
		channame := resolveChannelName(ctx, channelID, "")
		return parent, nil, channame
	}
	return slack.Message{}, fmt.Errorf("No such message found"), ""
}

func replacePermalinkWithText(ctx *IrcContext, text string) string {
	matches := rxSlackArchiveURL.FindStringSubmatch(text)
	if len(matches) != 4 {
		return text
	}
	channel := matches[1]
	timestamp := matches[2] + "." + matches[3]
	message, err, _ := getConversationDetails(ctx, channel, timestamp)
	if err != nil {
		log.Printf("could not get message details from permalink %s %s %s %v", matches[0], channel, timestamp, err)
		return text
	}
	return text + "\n> " + message.Text
}

func printMessage(ctx *IrcContext, message slack.Msg, prefix string) {
	user := ctx.GetUserInfo(message.User)
	name := ""
	if user == nil {
		if message.User != "" {
			log.Warningf("Failed to get user info for %v %s", message.User, message.Username)
			name = message.User
		} else {
			name = strings.ReplaceAll(message.Username, " ", "_")
		}
	} else {
		name = user.Name
	}
	// get channel or other recipient (e.g. recipient of a direct message)
	channame := resolveChannelName(ctx, message.Channel, message.ThreadTimestamp)

	text := message.Text
	for _, attachment := range message.Attachments {
		text = joinText(text, attachment.Pretext, "\n")
		text = joinText(text, attachment.Title, "\n")
		if attachment.Text != "" {
			text = joinText(text, attachment.Text, "\n")
		} else {
			text = joinText(text, attachment.Fallback, "\n")
		}
		text = joinText(text, attachment.ImageURL, "\n")
	}
	for _, file := range message.Files {
		text = joinText(text, ctx.FileHandler.Download(file), " ")
	}

	log.Debugf("SLACK msg from %v (%v) on %v: %v",
		message.User,
		name,
		message.Channel,
		text,
	)
	if name == "" && text == "" {
		log.Warningf("Empty username and message: %+v", message)
		return
	}
	text = replacePermalinkWithText(ctx, text)
	text = ctx.ExpandUserIds(text)
	text = ExpandText(text)
	text = joinText(prefix, text, " ")

	if name == ctx.Nick() {
		botID := message.BotID
		if (ctx.usingLegacyToken && user != nil && botID != user.Profile.BotID) ||
			(!ctx.usingLegacyToken && message.ClientMsgID == "") {
			// Don't print my own messages.
			// When using legacy tokens, we distinguish our own messages sent
			// from other clients by checking the bot ID.
			// With new style tokens, we check the client message ID.
			log.Debugf("Skipping message sent by me")
			return
		}
	}
	// handle multi-line messages
	var linePrefix, lineSuffix string
	if message.SubType == "me_message" {
		// handle /me messages
		linePrefix = "\x01ACTION "
		lineSuffix = "\x01"
	}
	for _, line := range strings.Split(text, "\n") {
		privmsg := fmt.Sprintf(":%v!%v@%v PRIVMSG %v :%s%s%s\r\n",
			name, message.User, ctx.ServerName,
			channame, linePrefix, line, lineSuffix,
		)
		log.Debug(privmsg)
		if _, err := ctx.Conn.Write([]byte(privmsg)); err != nil {
			log.Warningf("Failed to send IRC message: %v", err)
		}
	}
}

func eventHandler(ctx *IrcContext, rtm *slack.RTM) {
	log.Info("Started Slack event listener")
	for msg := range rtm.IncomingEvents {
		switch ev := msg.Data.(type) {
		case *slack.MessageEvent:
			// https://api.slack.com/events/message
			message := ev.Msg
			if message.Hidden {
				continue
			}
			switch message.SubType {
			case "message_changed":
				// https://api.slack.com/events/message/message_changed
				editedMessage, err, _ := getConversationDetails(ctx, message.Channel, message.Timestamp)
				if err != nil {
					fmt.Printf("could not get changed conversation details %s", err)
					continue
				}
				log.Printf("edited msg chan %v", editedMessage.Msg.Channel)
				editedMessage.Msg.Channel = message.Channel
				printMessage(ctx, editedMessage.Msg, "(edited)")
				continue
			case "channel_topic":
				// https://api.slack.com/events/message/channel_topic
				// Send out new topic
				channel := ctx.Channels.ByID(message.Channel)
				if channel == nil {
					log.Warningf("Cannot get channel name for %v", message.Channel)
				} else {
					newTopic := fmt.Sprintf(":%v TOPIC %s :%v\r\n", ctx.Mask(), channel.IRCName(), message.Topic)
					log.Infof("Got new topic: %v", newTopic)
					if _, err := ctx.Conn.Write([]byte(newTopic)); err != nil {
						log.Warningf("Failed to send IRC message: %v", err)
					}
				}
			case "channel_join", "channel_leave":
				// https://api.slack.com/events/message/channel_join
				// https://api.slack.com/events/message/channel_leave
				// Note: this is handled by slack.MemberJoinedChannelEvent
				// and slack.MemberLeftChannelEvent.
			default:
				printMessage(ctx, message, "")
			}
		case *slack.ConnectedEvent:
			log.Info("Connected to Slack")
			ctx.SlackConnected = true
		case *slack.DisconnectedEvent:
			de := msg.Data.(*slack.DisconnectedEvent)
			log.Warningf("Disconnected from Slack (intentional: %v, cause: %v)", de.Intentional, de.Cause)
			ctx.SlackConnected = false
			ctx.Conn.Close()
			ctx.Users, ctx.Channels = nil, nil
			return
		case *slack.MemberJoinedChannelEvent:
			// This is the currently preferred way to notify when a user joins a
			// channel, see https://api.slack.com/changelog/2017-05-rethinking-channel-entrance-and-exit-events-and-messages
			// https://api.slack.com/events/member_joined_channel
			log.Infof("Event: Member Joined Channel: %+v", ev)
			ch := ctx.Channels.ByID(ev.Channel)
			if ch == nil {
				log.Warningf("Unknown channel: %s", ev.Channel)
				continue
			}
			if _, err := ctx.Conn.Write([]byte(fmt.Sprintf(":%s JOIN %s\r\n", ctx.Mask(), ch.IRCName()))); err != nil {
				log.Warningf("Failed to send IRC JOIN message for `%s`: %v", ch.IRCName(), err)
			}
		case *slack.MemberLeftChannelEvent:
			// This is the currently preferred way to notify when a user leaves a
			// channel, see https://api.slack.com/changelog/2017-05-rethinking-channel-entrance-and-exit-events-and-messages
			// https://api.slack.com/events/member_left_channel
			log.Infof("Event: Member Left Channel: %+v", ev)
			ch := ctx.Channels.ByID(ev.Channel)
			if ch == nil {
				log.Warningf("Unknown channel: %s", ev.Channel)
				continue
			}
			if _, err := ctx.Conn.Write([]byte(fmt.Sprintf(":%v PART %s\r\n", ctx.Mask(), ch.IRCName()))); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
		case *slack.TeamJoinEvent:
			// https://api.slack.com/events/team_join
			// update the users list
			if _, err := ctx.Users.FetchByIDs(ctx.SlackClient, false, ev.User.ID); err != nil {
				log.Warningf("Failed to fetch users: %v", err)
			}
		case *slack.UserChangeEvent:
			// https://api.slack.com/events/user_change
			// update the user list
			if _, err := ctx.Users.FetchByIDs(ctx.SlackClient, false, ev.User.ID); err != nil {
				log.Warningf("Failed to fetch users: %v", err)
			}
		case *slack.ChannelJoinedEvent, *slack.ChannelLeftEvent:
			// https://api.slack.com/events/channel_joined
			// Note: this is handled by slack.MemberJoinedChannelEvent
			// and slack.MemberLeftChannelEvent.
		case *slack.ReactionAddedEvent:
			// https://api.slack.com/events/reaction_added
			user := ctx.GetUserInfo(ev.User)
			name := ""
			if user == nil {
				log.Warningf("Error getting user info for %v", ev.User)
				name = ev.User
			} else {
				name = user.Name
			}
			msg, err, channame := getConversationDetails(ctx, ev.Item.Channel, ev.Item.Timestamp)
			
			if err != nil {
				fmt.Printf("could not get Conversation details %s", err)
				continue
			}
			msgText := msg.Text

			msgText = ctx.ExpandUserIds(msgText)
			msgText = ExpandText(msgText)
			msgText = strings.Split(msgText, "\n")[0]

			msgText = msgText[:int(math.Min(float64(len(msgText)), 100))]

			privmsg := fmt.Sprintf(":%v!%v@%v PRIVMSG %v :\x01ACTION reacted with %s to: \x0315%s\x03\x01\r\n",
				name, ev.User, ctx.ServerName,
				channame, ev.Reaction, msgText,
			)
			log.Debug(privmsg)
			if _, err := ctx.Conn.Write([]byte(privmsg)); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
		case *slack.UserTypingEvent:
			// https://api.slack.com/events/user_typing
			u := ctx.GetUserInfo(ev.User)
			username := "<unknown>"
			if u != nil {
				username = u.Name
			}
			c, err := ctx.GetConversationInfo(ev.Channel)
			channame := "<unknown or IM chat>"
			if err == nil {
				channame = c.Name
			}
			log.Infof("User %s (%s) is typing on channel %s (%s)", ev.User, username, ev.Channel, channame)
		case *slack.DesktopNotificationEvent:
			// TODO implement actions on notifications
			log.Infof("Event: Desktop notification: %+v", ev)
		case *slack.LatencyReport:
			log.Infof("Current Slack latency: %v", ev.Value)
		case *slack.RTMError:
			log.Warningf("Slack RTM error: %v", ev.Error())
		case *slack.InvalidAuthEvent:
			log.Warningf("Invalid slack credentials")
		default:
			log.Debugf("SLACK event: %v: %+v", msg.Type, msg.Data)
		}
	}
}
