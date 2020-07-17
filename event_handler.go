package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/slack-go/slack"
)

func joinText(first string, second string, divider string) string {
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	return first + divider + second
}

func formatMultipartyChannelName(slackChannelID string, slackChannelName string) string {
	name := "&" + slackChannelID + "|" + slackChannelName
	name = strings.Replace(name, "mpdm-", "", -1)
	name = strings.Replace(name, "--", "-", -1)
	if len(name) >= 30 {
		return name[:29] + "…"
	}
	return name
}

func formatThreadChannelName(threadTimestamp string, channel *slack.Channel) string {
	return "+" + channel.Name + "-" + threadTimestamp
}

func resolveChannelName(ctx *IrcContext, msgChannel, threadTimestamp string) string {
	// channame := ""
	if strings.HasPrefix(msgChannel, "C") || strings.HasPrefix(msgChannel, "G") {
		// Channel message
		channel, err := ctx.GetConversationInfo(msgChannel)

		if err != nil {
			log.Warningf("Failed to get channel info for %v: %v", msgChannel, err)
			return ""
		} else if threadTimestamp != "" {
			channame := formatThreadChannelName(threadTimestamp, channel)
			_, ok := ctx.Channels[channame]
			if !ok {
				openingText, err := ctx.GetThreadOpener(msgChannel, threadTimestamp)
				if err == nil {
					IrcSendChanInfoAfterJoin(
						ctx,
						channame,
						msgChannel,
						openingText.Text,
						[]string{},
						true,
					)
				} else {
					log.Warningf("Didn't find thread channel %v", err)
				}

				user := ctx.GetUserInfo(openingText.User)
				name := ""
				if user == nil {
					log.Warningf("Error getting user info for %v", openingText.User)
					name = openingText.User
				} else {
					name = user.Name
				}

				privmsg := fmt.Sprintf(":%v!%v@%v PRIVMSG %v :%s%s%s\r\n",
					name, openingText.User, ctx.ServerName,
					channame, "", openingText.Text, "",
				)
				if _, err := ctx.Conn.Write([]byte(privmsg)); err != nil {
					log.Warningf("Failed to send IRC message: %v", err)
				}
			}
			return channame
		} else if channel.IsMpIM {
			channame := formatMultipartyChannelName(msgChannel, channel.Name)
			_, ok := ctx.Channels[channame]
			if !ok {
				IrcSendChanInfoAfterJoin(
					ctx,
					channame,
					msgChannel,
					channel.Purpose.Value,
					[]string{},
					true,
				)
			}
			return channame
		}

		return "#" + channel.Name
	} else if strings.HasPrefix(msgChannel, "D") {
		// Direct message to me
		users, err := usersInConversation(ctx, msgChannel)
		if err != nil {
			// ERR_UNKNOWNERROR
			if err := SendIrcNumeric(ctx, 400, ctx.Nick(), fmt.Sprintf("Cannot get conversation info for %s", msgChannel)); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
			return ""
		}
		// we expect only two members in a direct message. Raise an
		// error if not.
		if len(users) == 0 || len(users) > 2 {
			// ERR_UNKNOWNERROR
			if err := SendIrcNumeric(ctx, 400, ctx.Nick(), fmt.Sprintf("Want 1 or 2 users in conversation, got %d (conversation ID: %s)", len(users), msgChannel)); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
			return ""

		}
		// of the two users, one is me. Otherwise fail
		if ctx.UserID() == "" {
			// ERR_UNKNOWNERROR
			if err := SendIrcNumeric(ctx, 400, ctx.UserID(), "Cannot get my own user ID"); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
			return ""
		}
		user1 := users[0]
		var user2 string
		if len(users) == 2 {
			user2 = users[1]
		} else {
			// len is 1. Sending a message to myself
			user2 = user1
		}
		if user1 != ctx.UserID() && user2 != ctx.UserID() {
			// ERR_UNKNOWNERROR
			if err := SendIrcNumeric(ctx, 400, ctx.UserID(), fmt.Sprintf("Got a direct message where I am not part of the members list (members: %s)", strings.Join(users, ", "))); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
			return ""
		}
		var recipientID string
		if user1 == ctx.UserID() {
			// then it's the other user
			recipientID = user2
		} else {
			recipientID = user1
		}
		// now resolve the ID to the user's nickname
		nickname := ctx.GetUserInfo(recipientID)
		if nickname == nil {
			// ERR_UNKNOWNERROR
			if err := SendIrcNumeric(ctx, 400, ctx.UserID(), fmt.Sprintf("Unknown destination user ID %s for direct message %s", recipientID, msgChannel)); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
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
) (slack.Message, error) {
	message, err := ctx.SlackClient.GetConversationHistory(&slack.GetConversationHistoryParameters{
		ChannelID: channelID,
		Latest:    timestamp,
		Limit:     1,
		Inclusive: true,
	})
	if err != nil {
		return slack.Message{}, err
	}
	if len(message.Messages) > 0 {
		return message.Messages[0], nil
	}
	return slack.Message{}, fmt.Errorf("No such message found")
}

func replacePermalinkWithText(ctx *IrcContext, text string) string {
	matches := rxSlackArchiveURL.FindStringSubmatch(text)
	if len(matches) != 4 {
		return text
	}
	channel := matches[1]
	timestamp := matches[2] + "." + matches[3]
	message, err := getConversationDetails(ctx, channel, timestamp)
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
			if message.SubType == "message_changed" {
				editedMessage, err := getConversationDetails(ctx, message.Channel, message.Timestamp)
				if err != nil {
					fmt.Printf("could not get changed conversation details %s", err)
					continue
				}
				log.Printf("edited msg chan %v", editedMessage.Msg.Channel)
				editedMessage.Msg.Channel = message.Channel
				printMessage(ctx, editedMessage.Msg, "(edited)")
				continue
			}
			printMessage(ctx, message, "")

			// Check if the topic has changed
			if message.Topic != ctx.Channels[message.Channel].Topic {
				// Send out new topic
				channel, err := ctx.SlackClient.GetChannelInfo(message.Channel)
				if err != nil {
					log.Warningf("Cannot get channel name for %v", message.Channel)
				} else {
					newTopic := fmt.Sprintf(":%v TOPIC #%v :%v\r\n", ctx.Mask(), channel.Name, message.Topic)
					log.Infof("Got new topic: %v", newTopic)
					if _, err := ctx.Conn.Write([]byte(newTopic)); err != nil {
						log.Warningf("Failed to send IRC message: %v", err)
					}
				}
			}

			// check if new people joined the channel
			added, removed := ctx.Channels[message.Channel].MembersDiff(message.Members)
			if len(added) > 0 || len(removed) > 0 {
				log.Infof("[*] People who joined: %v", added)
				log.Infof("[*] People who left: %v", removed)
			}
		case *slack.ConnectedEvent:
			log.Info("Connected to Slack")
			ctx.SlackConnected = true
		case *slack.DisconnectedEvent:
			de := msg.Data.(*slack.DisconnectedEvent)
			log.Warningf("Disconnected from Slack (intentional: %v, cause: %v)", de.Intentional, de.Cause)
			ctx.SlackConnected = false
			ctx.Conn.Close()
			return
		case *slack.MemberJoinedChannelEvent, *slack.MemberLeftChannelEvent:
			// https://api.slack.com/events/member_joined_channel
			// https://api.slack.com/events/member_left_channel
			// FIXME send a JOIN / PART message to the IRC client
			log.Infof("Event: Member Joined/Left Channel: %+v", ev)
		case *slack.TeamJoinEvent, *slack.UserChangeEvent:
			// https://api.slack.com/events/team_join
			// https://api.slack.com/events/user_change
			// Refresh the users list
			// TODO update just the new user
			ctx.GetUsers(true)
		case *slack.ChannelJoinedEvent:
			// https://api.slack.com/events/channel_joined
			if _, err := ctx.Conn.Write([]byte(fmt.Sprintf(":%v JOIN #%v\r\n", ctx.Mask(), ev.Channel.Name))); err != nil {
				log.Warningf("Failed to send IRC message: %v", err)
			}
		case *slack.ReactionAddedEvent:
			// https://api.slack.com/events/reaction_added
			channame := resolveChannelName(ctx, ev.Item.Channel, "")
			user := ctx.GetUserInfo(ev.User)
			name := ""
			if user == nil {
				log.Warningf("Error getting user info for %v", ev.User)
				name = ev.User
			} else {
				name = user.Name
			}
			msg, err := getConversationDetails(ctx, ev.Item.Channel, ev.Item.Timestamp)
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
