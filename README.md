# IRC-to-Slack gateway

`irc-slack` is an IRC-to-Slack gateway. It is an IRC server that lets you
connect to your Slack teams with your IRC client.

[![](images/team_chat_2x.png)](https://xkcd.com/1782/)

(That guy is me)

Slack has ended support for IRC and XMPP gateway on the 15th of May 2018. So
what's left to do for people like me, who want to still be able to log in via
IRC? Either you use [wee-slack](https://github.com/wee-slack/wee-slack) (~~but I
don't use WeeChat~~), or you implement your own stuff.

NOTE: after Slack turned down their IRC gateway I got a lot of contacts from users of irc-slack asking me to fix and improve it. I didn't expect people to actually use it, but thanks to your feedback I'm now actively developing it again :-)
Please keep reporting bugs and sending PRs!

## How to use it

```
cd cmd/irc-slack
make # use `make` instead of `go build` to include build information when running with `-v`
./irc-slack # by default on port 6666
```

Then configure your IRC client to connect to localhost:6666 and use one of the methods in the Tokens section to set the connection password.

You can also [run it with Docker](#run-it-with-docker).

## Feature matrix

|     | public channel | private channel | multiparty IM | IM |
| --- | --- | --- | --- | --- |
| from me | works | works | doesn't work ([#168](https://github.com/insomniacslk/irc-slack/issues/168)) | works |
| to me | works | works | works | works |
| thread from me | doesn't work ([#168](https://github.com/insomniacslk/irc-slack/issues/168)) | doesn't work ([#168](https://github.com/insomniacslk/irc-slack/issues/168)) | untested | doesn't work ([#166](https://github.com/insomniacslk/irc-slack/issues/166)) |
| thread to me | works | works | untested | works but sends in the IM chat ([#167](https://github.com/insomniacslk/irc-slack/issues/167)) |

## Encryption

`irc-slack` by default does not use encryption when communicating with your IRC
client (but the communication between `irc-slack` and the Slack servers is
encrypted).
If you want to use TLS, you can use the `-key` and `-cert` command line
parameters, and point them to a TLS certificate that you own.
This is useful if you plan to connect to to `irc-slack` over the internet.

For example, you can generate a valid certificate with LetsEncrypt (adjust the relevant
fields of course):
```
sudo certbot certonly \
    -n \
    -d your.domain.example.com \
    --test-cert \
    --standalone \
    -m your@email.example.com \
    --agree-tos
```

Then your key and certificate will be generated under
`/etc/letsencrypt/live/your.domain.example.com`
with the names `privkey.pem` and `cert.pem` respectively.

## Authentication

To connect to Slack via `irc-slack` you need an authentication string. There are
three possible methods:
* User tokens with auth cookies (recommended)
* Slack app tokens (if you can install apps on your slack team)
* legacy tokens (soon to be deprecated)

These options are discussed in more detail below.
Then just add `-key <path/to/privkey.pem> -cert <path/to/cert.pem>` to enable
TLS on `irc-slack`, and enable TLS on your IRC client.


### User tokens with auth cookie

This approach does not require legacy tokens nor installing any app, but in order to
get the token there are a few manual steps to execute.

This type of token starts with `xoxc-`, and requires an auth cookie to be paired
to it in order to work.

There are two possible procedures, an entirely manual one, using the browser
console, and a semi-automated one, which requires Chrome or Chromium in headless
mode.

**manual procedure via browser**

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

and use the above as your IRC password.

**semi-automated procedure using Chrome/Chromium in headless mode**

See [autotoken](tools/autotoken). Just build it with `go build` and run with
`./autotoken -h` to see the usage help.

If you prefer to run `autotoken` via Docker, you can test your luck with:
```
docker build -t insomniacslk/irc-slack/tools-autotoken -f Dockerfile.autotoken .
docker run --rm -it -e DISPLAY=$DISPLAY -v /tmp/.X11-unix:/tmp/.X11-unix insomniacslk/irc-slack/tools-autotoken autotoken -h
```

### Slack App tokens

As an alternative, you can install the irc-slack app on your workspace, and use the token that it returns after you authorize it.

In order to run the application, you need to do the following steps:
* create a Slack app using their v1 OauthV2 API (note: not their v2 version) at https://api.slack.com/apps
* configure the redirect URL to your endpoint (in this case
  https://my-server/irc-slack/auth/)
* run the web app under [slackapp](tools/slackapp/) passing your app client ID and client secret, you can find them in the Basic Information tab at the link at the previous step

The token starts with `xoxp-`, and you can use it as your IRC password when
connecting to `irc-slack`.

This is a Slack app with full user permissions, that is used to generate a Slack user token.
Note that you need to install this app on every workspace you want to use it
for, and the workspace owners may reject it.

This app exchanges your temporary authentication code with a permanent token.

### Legacy tokens

This is the easiest method, but it's deprecated and Slack will soon disable it.
Slack has announced that they will stop issuing legacy tokens starting the 4th
of May 2020, so this section will stay here for historical reasons.

Get you Slack legacy token at https://api.slack.com/custom-integrations/legacy-tokens ,
and set it as your IRC password when connecting to `irc-slack`.


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
/network add yourteam.slack.com
/server add -auto -network yourteam.slack.com localhost 6666 xoxp-<your-slack-token>
/connect yourteam.slack.com
```

Remember to add `-tls` to the `/connect` command if you're running `irc-slack`
with TLS.
Also remember to replace `localhost` with the name of the host you're connecting to,
if different.

### Connecting with WeeChat

```
/server add yourteam.slack.com localhost/6666
/set irc.server.yourteam.slack.com.password xoxp-<your-slack-token>
/connect yourteam.slack.com
```

To enable TLS, also run the following before the `/connect` command:
```
/set irc.server.yourteam.slack.com.ssl on
/set irc.server.yourteam.slack.com.ssl_verify on
```

Also remember to replace `localhost` with the name of the host you're connecting to,
if different.

## Gateway usage

There are a few options that you can pass to the server, e.g. to change the listener port, or the server name:

```
$ ./irc-slack -h
Usage of ./irc-slack:
  -c, --cert string         TLS certificate for HTTPS server. Requires -key
  -C, --chunk int           Maximum size of a line to send to the client. Only works for certain reply types (default 512)
  -D, --debug               Enable debug logging of the Slack API
  -d, --download string     If set will download attachments to this location
  -l, --fileprefix string   If set will overwrite urls to attachments with this prefix and local file name inside the path set with -d
  -H, --host string         IP address to listen on (default "127.0.0.1")
  -k, --key string          TLS key for HTTPS server. Requires -cert
  -L, --loglevel string     Log level. One of [none debug info warning error fatal] (default "info")
  -P, --pagination int      Pagination value for API calls. If 0 or unspecified, use the recommended default (currently 200). Larger values can help on large Slack teams
  -p, --port int            Local port to listen on (default 6666)
  -s, --server string       IRC server name (i.e. the host name to send to clients)
pflag: help requested
exit status 2
```

## Deploying with Puppet

You can use the [irc-slack module for Puppet](https://github.com/b4ldr/puppet-irc_slack) by [John Bond](https://github.com/b4ldr).

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
