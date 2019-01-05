############################
# STEP 1 build executable binary
############################
FROM golang:alpine AS builder
# Install git.
# Git is required for fetching the dependencies.
RUN apk update && apk add --no-cache git
COPY . $GOPATH/src/insomniacslk/irc-slack
WORKDIR $GOPATH/src/insomniacslk/irc-slack
# Fetch dependencies.
# Using go get.
RUN go get -d -v
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
# Run the irc-slack binary.
ENTRYPOINT ["/go/bin/irc-slack"]
