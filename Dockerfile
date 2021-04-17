############################
# STEP 1 build executable binary
############################
FROM golang:1.16-alpine AS builder

LABEL BUILD="docker build -t insomniacslk/irc-slack -f Dockerfile ."
LABEL RUN="docker run --rm -p 6666:6666 -it insomniacslk/irc-slack"

# Install git.
# Git is required for fetching the dependencies.
RUN apk update && apk add --no-cache git bash
COPY . $GOPATH/src/github.com/insomniacslk/irc-slack
ENV GO111MODULE=on
WORKDIR $GOPATH/src/github.com/insomniacslk/irc-slack/cmd/irc-slack
# Build the binary.
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o /go/bin/irc-slack

############################
# STEP 2 build a small image
############################
FROM scratch
# Copy the ssl certs so we can talk to slack
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
# Copy our static executable.
COPY --from=builder /go/bin/irc-slack /go/bin/irc-slack
ENV PATH="/go/bin:$PATH"
# Run the irc-slack binary.
CMD ["/go/bin/irc-slack", "-H", "0.0.0.0"]
