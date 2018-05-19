# IRC-to-Slack gateway

`irc-slack` is an IRC-to-Slack gateway. Run it locally, and it will spawn an IRC
server that will let you use your Slack teams via IRC.

[![](https://imgs.xkcd.com/comics/team_chat_2x.png)](https://xkcd.com/1782/)

(That guy is me)

Slack has ended support for IRC and XMPP gateway on the 15th of April 2018. So
what's left to do for people like me, who want to still be able to log in via
IRC? Either you use [wee-slack](https://github.com/wee-slack/wee-slack) (but I
don't use WeeChat), or you implement your own stuff.

The code quality is currently at the `works-for-me` level, but it's improving steadily.

NOTE: after Slack turned down their IRC gateway I got a lot of contacts from users of irc-slack asking me to fix and improve it. I didn't expect people to actually use it, but thanks to your feedback I'm now actively developing it again :-)
Please keep reporting bugs and sending PRs!

## How to use it

```
go get ./...  # download the dependencies, currently just github.com/nlopes/slack
go build
./irc-slack # by default on port 6666
```

Then configure your IRC client to connect to localhost:6666 and use a Slack legacy token as password. Example for irssi:

```
/network add SlackYourTeamName
/server add -auto SlackYourTeamName localhost 6666 xoxp-<your-slack-token>
```

Get you Slack legacy token at https://api.slack.com/custom-integrations/legacy-tokens .


You can also specify the port to listen on, and the server name, e.g.
your-team-name.slack.com.

```
$ ./irc-slack -h
Usage of ./irc-slack:
  -p int
        Local port to listen on (default 6666)
  -s string
        IRC server name (i.e. the host name to send to clients)
```


## TODO

A lot of things. Want to help? Grep "TODO", "FIXME" and "XXX" in the code and send me a PR :)

This currently "works for me", but I published it in the hope that someone would use it so we can find and fix bugs.

## BUGS

Plenty of them. I wrote this project while on a plane (like many other projects of mine) so this is hack-level quality - no proper design, no RFC compliance, no testing. I just fired up an IRC client until I could reasonably chat on a few Slack teams. Please report all the bugs you find on the Github issue tracker, or privately to me.

## How do I contact you?

Find my contacts on https://insomniac.slackware.it .

## Thanks

Special thanks to Stefan Stasik for helping me troubleshoot a zillion of bugs and misbehaviours :)
