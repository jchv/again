FROM golang
RUN wget -O /usr/local/bin/dumb-init https://github.com/Yelp/dumb-init/releases/download/v1.2.1/dumb-init_1.2.1_amd64

ADD . /go/src/github.com/jchv/again
RUN go install github.com/jchv/again
RUN chmod +x /usr/local/bin/dumb-init
WORKDIR /go/src/github.com/jchv/again
ENTRYPOINT ["/usr/local/bin/dumb-init", "--", "/go/bin/again"]
