FROM golang:alpine as builder
ARG LDFLAGS=""

# Install necessary packages
RUN apk --update --no-cache add git build-base gcc

# Copy the source code and build the application
COPY . /build
WORKDIR /build
RUN go build -ldflags "${LDFLAGS}" ./cmd/telegraf

FROM alpine:latest

# Create the telegraf user and necessary directories
RUN apk update --no-cache && \
    adduser -S -D -H -h / telegraf && \
    mkdir -p /etc/telegraf /var/metadata /var/cert /etc/telegraf/telegraf.d

# Copy the telegraf.version file and set ownership to the telegraf user
COPY --chown=telegraf:telegraf telegraf.version /var/metadata/telegraf.version

# Copy the built telegraf binary from the builder stage
COPY --from=builder /build/telegraf /

# Ensure the telegraf user owns the necessary directories
RUN chown -R telegraf:telegraf /etc/telegraf /var/metadata /var/cert /etc/telegraf/telegraf.d

# Switch to the telegraf user
USER telegraf

# Set the entrypoint
ENTRYPOINT ["./telegraf"]
