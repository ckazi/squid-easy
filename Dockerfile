FROM alpine:latest

WORKDIR /app

RUN apk update && apk add --no-cache apache2-utils && rm -rf /var/cache/apk/*

COPY squid-easy ./squid-easy
COPY templates ./templates/
COPY config ./config/

RUN chmod +x ./squid-easy

CMD ["./squid-easy"]