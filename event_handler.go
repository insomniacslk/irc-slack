package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/nlopes/slack"
)

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
}
