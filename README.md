# IRC-to-Slack gateway

`irc-slack` is an IRC-to-Slack gateway. Run it locally, and it will spawn an IRC
server that will let you use your Slack teams via IRC.

[![](images/team_chat_2x.png)](https://xkcd.com/1782/)

(That guy is me)

Slack has ended support for IRC and XMPP gateway on the 15th of May 2018. So
what's left to do for people like me, who want to still be able to log in via
IRC? Either you use [wee-slack](https://github.com/wee-slack/wee-slack) (but I
don't use WeeChat), or you implement your own stuff.

The code quality is currently at the `works-for-me` level, but it's improving steadily.

NOTE: after Slack turned down their IRC gateway I got a lot of contacts from users of irc-slack asking me to fix and improve it. I didn't expect people to actually use it, but thanks to your feedback I'm now actively developing it again :-)
Please keep reporting bugs and sending PRs!

## How to use it

```
go get ./...  # download the dependencies, currently just github.com/slack-go/slack
go build
./irc-slack # by default on port 6666
```

Then configure your IRC client to connect to localhost:6666 and use one of the methods in the Tokens section to set the connection password.

## Tokens

### Legacy tokens

This is the easiest method, but it's deprecated and Slack will disable it.
Slack has announced that they will stop issuing legacy tokens starting the 4th
of May 2020, so this section will stay here for historical reasons.

Get you Slack legacy token at https://api.slack.com/custom-integrations/legacy-tokens ,
and set it as your IRC password when connecting to `irc-slack`.

### Slack App tokens

As an alternative to legacy tokens, you can install the irc-slack app on your workspace, and use the token that it returns
after you authorize it. The source code of the app is available in this
repository, see below for details.

The token starts with `xoxp-`, and you can use it as your IRC password when
connecting to `irc-slack`.

This is a Slack app with full user permissions, that is used to generate a Slack user token.
Note that you need to install this app on every workspace you want to use it
for, and the workspace owners may reject it.

Click on the button below to install the app:

[![Authorize irc-slack](https://platform.slack-edge.com/img/add_to_slack.png)](https://slack.com/oauth/authorize?client_id=152572391990.1078733520672&scope=client)

Then copy the token from the resulting page (it starts with `xoxp-`).

This app exchanges your temporary authentication code with a permanent token.
While the app does not log your token, you may rightfully not trust it or want
to run your own. In order to do so, you need the two following steps:
* create a Slack app using their v1 OauthV2 API (note: not their v2 version) at https://api.slack.com/apps
* configure the redirect URL to your endpoint (in this case
  https://my-server/irc-slack/auth/)
* run the web app under [slackapp](slackapp/) passing your app client ID and client secret, you can find them in the Basic Information tab at the link at the previous step


### User tokens with auth cookie

This is still a work in progress. It does not require legacy tokens nor
installing any app, but getting the token requires to execute a few manual
steps in your browser's console.

This type of token starts with `xoxc-`, and requires an auth cookie to be paired
to it in order to work.

This is the same procedure as described in two similar projects, see:
* https://github.com/adsr/irslackd/wiki/IRC-Client-Config#xoxc-tokens
* https://github.com/ltworf/localslackirc/#obtain-a-token

But in short, log via browser on the Slack team, open the browser's network tab
in the developer tools, and look for an XHR transaction. Then look for
* the token (it starts with `xoxc-`) in the request data
* the auth cookie, contained in the `d` key-value in the request cookies (it looks like `d=XXXX;`)

Then concatenate the token and the auth cookie using a `|` character, like this:
```
xoxc-XXXX|d=XXXX;
```

and use the above as your connection password.


## Run it with Docker

Thanks to [halkeye](https://github.com/halkeye) you can run `irc-slack` via
Docker. The `Dockerfile` is published on
https://hub.docker.com/r/insomniacslk/irc-slack and will by default listen on
`0.0.0.0:6666`. You can pull and run it with:

```
docker run --rm -p 6666:6666 insomniacslk/irc-slack
```

If you want to build it locally, just run:
```
docker build -f Dockerfile . -t insomniacslk/irc-slack
```


### Connecting with irssi
```
/network add SlackYourTeamName
/server add -auto -network SlackYourTeamName localhost 6666 xoxp-<your-slack-token>
```


### Connecting with WeeChat

```
/server add yourteam.slack.com localhost/6666
/set irc.server.yourteam.slack.com.password xoxp-<your-slack-token>
```

## Gateway usage

There are a few options that you can pass to the server, e.g. to change the listener port, or the server name:

```
$ ./irc-slack -h
Usage of ./irc-slack:
  -H string
        IP address to listen on (default "127.0.0.1")
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

## Authors

* [Andrea Barberio](https://insomniac.slackware.it)
* [Josip Janzic](https://github.com/janza)

## Thanks

Special thanks to
* Stefan Stasik for helping me find, fix and troubleshoot a zillion of bugs :)
* [Mauro Codella](https://github.com/codella) for patiently reading and replying for two hours in a private conversation that I used to test the fix at [pull/23](https://github.com/insomniacslk/irc-slack/pull/23) :D
