package main

import (
	"fmt"
	"log"
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

func eventHandler(ctx *IrcContext, rtm *slack.RTM) {
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
				users, err := usersInConversation(ctx, ev.Msg.Channel)
				if err != nil {
					// ERR_UNKNOWNERROR
					SendIrcNumeric(ctx, 400, ctx.Nick(), fmt.Sprintf("Cannot get conversation info for %s", ev.Msg.Channel))
					continue
				}
				// we expect only two members in a direct message. Raise an
				// error if not.
				if len(users) != 2 {
					// ERR_UNKNOWNERROR
					SendIrcNumeric(ctx, 400, ctx.Nick(), fmt.Sprintf("Exactly two users expected in direct message, got %d (conversation ID: %s)", len(users), ev.Msg.Channel))
					continue

				}
				// of the two users, one is me. Otherwise fail
				if ctx.UserID() == "" {
					// ERR_UNKNOWNERROR
					SendIrcNumeric(ctx, 400, ctx.UserID(), "Cannot get my own user ID")
					continue
				}
				if users[0] != ctx.UserID() && users[1] != ctx.UserID() {
					// ERR_UNKNOWNERROR
					SendIrcNumeric(ctx, 400, ctx.UserID(), fmt.Sprintf("Got a direct message where I am not part of the members list (members: %s)", strings.Join(users, ", ")))
					continue
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
					SendIrcNumeric(ctx, 400, ctx.UserID(), fmt.Sprintf("Unknown destination user ID %s for direct message %s", recipientID, ev.Msg.Channel))
					continue
				}
				channame = nickname.Name
			} else {
				log.Printf("Unknown recipient ID: %s", ev.Msg.Channel)
				continue
			}

			text := ev.Msg.Text
			for _, attachment := range ev.Msg.Attachments {
				text = joinText(text, attachment.Pretext, "\n")
				if attachment.Text != "" {
					text = joinText(text, attachment.Text, "\n")
				} else {
					text = joinText(text, attachment.Fallback, "\n")
				}
				text = joinText(text, attachment.ImageURL, "\n")
			}
			for _, file := range ev.Msg.Files {
				text = joinText(text, file.URLPrivate, " ")
			}

			log.Printf("SLACK msg from %v (%v) on %v: %v",
				ev.Msg.User,
				name,
				ev.Msg.Channel,
				text,
			)
			if ev.Msg.User == "" && text == "" {
				log.Printf("WARNING: empty user and message: %+v", ev.Msg)
				continue
			}
			// replace UIDs with user names
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
			if name == ctx.Nick() && botID != user.Profile.BotID {
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
			log.Printf("SLACK event: %v: %+v", msg.Type, msg.Data)
		}
	}
}
