FROM golang:alpine as builder
ARG LDFLAGS=""
ARG VERSION="1.0.9"

RUN apk --update --no-cache add git build-base gcc

COPY . /build
WORKDIR /build

RUN go build -ldflags "${LDFLAGS} -X main.version=${VERSION}" ./cmd/telegraf

FROM alpine:latest
LABEL version="1.0.9"

RUN apk update --no-cache && \
    adduser -S -D -H -h / telegraf
USER 0
RUN mkdir -p /etc/telegraf /var/metadata /var/cert /etc/telegraf/telegraf.d

USER telegraf
COPY --from=builder /build/telegraf /

ENTRYPOINT ["./telegraf"]
