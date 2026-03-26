#!/usr/bin/env bash
# create self signed certs for dev
mkdir -p certs
openssl req -x509 -nodes -days 9999 -newkey rsa:2048 -keyout certs/selfsigned.key -out certs/selfsigned.crt -subj "/CN=localhost"
