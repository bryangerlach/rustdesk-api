# rustdesk api server with integrated client builder

The docker compose file will include the rustdesk server and rustdesk api server with client builder all in one. You can set a DVForge server and an rdgen server. If you do not run your own client builder server, you can use https://rdgen.crayoneater.org as the rdgen server, this is the publicly hosted rdgen server.

## docker-compose

Use the docker-compose-client-builder.yml file to start the servers.

Edit the environment variables and volumes to match your needs:

## environment:

### - RUSTDESK_API_KEY_FILE=/data/id_ed25519.pub

Set this to the location of your rustdesk key, this is the location inside the container, it is probably best to leave this as default

### - RUSTDESK_API_RUSTDESK_UPDATE_KEY_FILE=/data/update_signing_key.pem

This is an optional update signing key to verify updates. Automatic updates are currently a work in progress, and this may not work yet.

## volumes:
### - /opt/rustdesk/server:/data

Change the left side of this to match the server directory on your host machine, leave the right side untouched.

### - /opt/rustdesk/api:/app/data

Change the left side of this to match the api directory on your host machine, leave the right side untouched.

### - /opt/rustdesk/resources/public/upload/client_build:/app/resources/public/upload/client_build

Change the left side of this to match the clients directory on your host machine, leave the right side untouched.