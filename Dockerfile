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
RUN mkdir -p /etc/telegraf
RUN mkdir -p /var/metadata
ADD telegraf.version /var/metadata
RUN mkdir -p /var/cert
RUN mkdir -p /etc/telegraf/telegraf.d 
USER telegraf
COPY --from=builder /build/telegraf /


EXPOSE 50051/udp

ENTRYPOINT ["./telegraf"]
