# rustdesk api server with integrated client builder

The docker compose file will include the rustdesk server and rustdesk api server with client builder all in one. You can set a DVForge server and an rdgen server. If you do not run your own client builder server, you can use https://rdgen.crayoneater.org as the rdgen server, this is the publicly hosted rdgen server.

## docker-compose

Use the docker-compose-client-builder.yml file to start the servers.

Edit the environment variables and volumes to match your needs.