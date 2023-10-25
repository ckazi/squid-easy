FROM ubuntu:latest

WORKDIR /app

RUN apt-get update && apt-get install -y apache2-utils

COPY squid-easy ./squid-easy
COPY templates ./templates/
COPY config ./config/

RUN apt-get clean && rm -rf /var/lib/apt/lists/*

RUN chmod +x ./squid-easy

ENTRYPOINT ["./squid-easy"]