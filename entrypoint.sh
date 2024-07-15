#!/bin/sh

# Ensure the version file is copied to the host mount if it doesn't exist
if [ ! -f /var/metadata/telegraf.version ]; then
   su-exec root cp /telegraf.version /var/metadata/telegraf.version
fi

# Execute the original entrypoint
exec "$@"
