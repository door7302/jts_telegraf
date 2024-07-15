FROM golang:alpine as builder
ARG LDFLAGS=""

RUN apk --update --no-cache add git build-base gcc

COPY . /build
WORKDIR /build

RUN go build -ldflags "${LDFLAGS}" ./cmd/telegraf

FROM alpine:latest

RUN apk update --no-cache && \
    adduser -S -D -H -h / telegraf
USER 0
RUN mkdir -p /etc/telegraf /var/metadata /var/cert /etc/telegraf/telegraf.d
COPY telegraf.version /var/metadata/telegraf.version

USER telegraf
COPY --from=builder /build/telegraf /



ENTRYPOINT ["./telegraf"]
