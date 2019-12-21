package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/nlopes/slack"
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
				ctx.Conn.Write([]byte(privmsg))
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
			SendIrcNumeric(ctx, 400, ctx.Nick(), fmt.Sprintf("Cannot get conversation info for %s", msgChannel))
			return ""
		}
		// we expect only two members in a direct message. Raise an
		// error if not.
		if len(users) != 2 {
			// ERR_UNKNOWNERROR
			SendIrcNumeric(ctx, 400, ctx.Nick(), fmt.Sprintf("Exactly two users expected in direct message, got %d (conversation ID: %s)", len(users), msgChannel))
			return ""

		}
		// of the two users, one is me. Otherwise fail
		if ctx.UserID() == "" {
			// ERR_UNKNOWNERROR
			SendIrcNumeric(ctx, 400, ctx.UserID(), "Cannot get my own user ID")
			return ""
		}
		if users[0] != ctx.UserID() && users[1] != ctx.UserID() {
			// ERR_UNKNOWNERROR
			SendIrcNumeric(ctx, 400, ctx.UserID(), fmt.Sprintf("Got a direct message where I am not part of the members list (members: %s)", strings.Join(users, ", ")))
			return ""
		}
		var recipientID string
		if users[0] == ctx.UserID() {
			// then it's the other user
			recipientID = users[1]
		} else {
			recipientID = users[0]
		}
		// now resolve the ID to the user's nickname
		nickname := ctx.GetUserInfo(recipientID)
		if nickname == nil {
			// ERR_UNKNOWNERROR
			SendIrcNumeric(ctx, 400, ctx.UserID(), fmt.Sprintf("Unknown destination user ID %s for direct message %s", recipientID, msgChannel))
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

func eventHandler(ctx *IrcContext, rtm *slack.RTM) {
	log.Info("Started Slack event listener")
	var messageCache []slack.Msg
	for msg := range rtm.IncomingEvents {
		switch ev := msg.Data.(type) {
		case *slack.MessageEvent:
			user := ctx.GetUserInfo(ev.Msg.User)
			name := ""
			if user == nil {
				log.Warningf("Failed to get user info for %v", ev.Msg.User)
				name = ev.Msg.User
			} else {
				name = user.Name
			}
			// get channel or other recipient (e.g. recipient of a direct message)
			channame := resolveChannelName(ctx, ev.Msg.Channel, ev.ThreadTimestamp)

			text := ev.Msg.Text
			for _, attachment := range ev.Msg.Attachments {
				text = joinText(text, attachment.Pretext, "\n")
				text = joinText(text, attachment.Title, "\n")
				if attachment.Text != "" {
					text = joinText(text, attachment.Text, "\n")
				} else {
					text = joinText(text, attachment.Fallback, "\n")
				}
				text = joinText(text, attachment.ImageURL, "\n")
			}
			for _, file := range ev.Msg.Files {
				text = joinText(text, ctx.FileHandler.Download(file), " ")
			}

			log.Debugf("SLACK msg from %v (%v) on %v: %v",
				ev.Msg.User,
				name,
				ev.Msg.Channel,
				text,
			)
			if ev.Msg.User == "" && text == "" {
				log.Warningf("Empty user and message: %+v", ev.Msg)
				continue
			}
			text = ctx.ExpandUserIds(text)
			text = ExpandText(text)
			messageCache = appendIfNotMoreThan(messageCache, ev.Msg)

			// FIXME if two instances are connected to the Slack API at the
			// same time, this will hide the other instance's message
			// believing it was sent from here. But since it's not, both
			// local echo and remote message won't be shown
			botID := msg.Data.(*slack.MessageEvent).BotID
			if name == ctx.Nick() && user != nil && botID != user.Profile.BotID {
				// don't print my own messages
				continue
			}
			// handle multi-line messages
			var linePrefix, lineSuffix string
			if ev.Msg.SubType == "me_message" {
				// handle /me messages
				linePrefix = "\x01ACTION "
				lineSuffix = "\x01"
			}
			for _, line := range strings.Split(text, "\n") {
				privmsg := fmt.Sprintf(":%v!%v@%v PRIVMSG %v :%s%s%s\r\n",
					name, ev.Msg.User, ctx.ServerName,
					channame, linePrefix, line, lineSuffix,
				)
				log.Debug(privmsg)
				ctx.Conn.Write([]byte(privmsg))
			}
			msgEv := msg.Data.(*slack.MessageEvent)
			// Check if the topic has changed
			if msgEv.Topic != ctx.Channels[msgEv.Channel].Topic {
				// Send out new topic
				channel, err := ctx.SlackClient.GetChannelInfo(msgEv.Channel)
				if err != nil {
					log.Warningf("Cannot get channel name for %v", msgEv.Channel)
				} else {
					newTopic := fmt.Sprintf(":%v TOPIC #%v :%v\r\n", ctx.Mask(), channel.Name, msgEv.Topic)
					log.Infof("Got new topic: %v", newTopic)
					ctx.Conn.Write([]byte(newTopic))
				}
			}
			// check if new people joined the channel
			added, removed := ctx.Channels[msgEv.Channel].MembersDiff(msgEv.Members)
			if len(added) > 0 || len(removed) > 0 {
				log.Infof("[*] People who joined: %v", added)
				log.Infof("[*] People who left: %v", removed)
			}
		case *slack.ConnectedEvent:
			log.Info("Connected to Slack")
			ctx.SlackConnected = true
		case *slack.DisconnectedEvent:
			log.Warningf("Disconnected from Slack (intentional: %v)", msg.Data.(*slack.DisconnectedEvent).Intentional)
			ctx.SlackConnected = false
			ctx.Conn.Close()
			return
		case *slack.MemberJoinedChannelEvent, *slack.MemberLeftChannelEvent:
			// refresh the users list
			// FIXME also send a JOIN / PART message to the IRC client
			ctx.GetUsers(true)
		case *slack.ChannelJoinedEvent:
			ctx.Conn.Write([]byte(fmt.Sprintf(":%v JOIN #%v\r\n", ctx.Mask(), ev.Channel.Name)))
		case *slack.ReactionAddedEvent:
			channame := resolveChannelName(ctx, ev.Item.Channel, "")
			user := ctx.GetUserInfo(ev.User)
			name := ""
			if user == nil {
				log.Warningf("Error getting user info for %v", ev.User)
				name = ev.User
			} else {
				name = user.Name
			}
			msgText := ""
			for _, msg := range messageCache {
				if msg.Timestamp == ev.Item.Timestamp {
					msgText = msg.Text
					break
				}
			}
			if msgText == "" {
				msg, err := getConversationDetails(ctx, ev.Item.Channel, ev.Item.Timestamp)
				if err != nil {
					fmt.Printf("could not get Conversation details %s", err)
				}
				msgText = msg.Text
			}

			msgText = ctx.ExpandUserIds(msgText)
			msgText = ExpandText(msgText)

			msgText = msgText[:int(math.Min(float64(len(msgText)), 100))]

			privmsg := fmt.Sprintf(":%v!%v@%v PRIVMSG %v :\x01ACTION reacted with %s to: \x0315%s\x03\x01\r\n",
				name, ev.User, ctx.ServerName,
				channame, ev.Reaction, msgText,
			)
			log.Debug(privmsg)
			ctx.Conn.Write([]byte(privmsg))
		case *slack.DesktopNotificationEvent:
			// TODO implement actions on notifications
			log.Infof("Desktop notification: %+v", ev)
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
