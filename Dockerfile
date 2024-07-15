FROM golang:alpine as builder
ARG LDFLAGS=""

# Install necessary packages
RUN apk --update --no-cache add git build-base gcc

# Copy the source code and build the application
COPY . /build
WORKDIR /build
RUN go build -ldflags "${LDFLAGS}" ./cmd/telegraf

FROM alpine:latest

USER 0 
# Create the telegraf user and necessary directories
RUN apk update --no-cache && \
    adduser -S -D -H -h / telegraf && \
    mkdir -p /etc/telegraf /var/cert /etc/telegraf/telegraf.d

# Copy the built telegraf binary from the builder stage
COPY --from=builder /build/telegraf /

# Switch to the telegraf user
USER telegraf
RUN mkdir /var/metadata

# Copy the telegraf.version file and set ownership to the telegraf user
COPY telegraf.version /var/metadata/telegraf.version

# Set the entrypoint
ENTRYPOINT ["./telegraf"]
