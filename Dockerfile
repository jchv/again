FROM golang
ADD . /go/src/github.com/jchv/again
RUN go install github.com/jchv/again
WORKDIR /go/src/github.com/jchv/again
ENTRYPOINT ["/go/bin/again"]
