FROM alpine:3.17 as builder
LABEL stage=go-builder
WORKDIR /app/
COPY ./ ./
RUN apk add --no-cache bash curl gcc git go upx musl-dev; \
    bash build.sh release docker 

FROM alpine:3.17
RUN set -ex \
  && apk add --update --no-cache \
     sqlite unzip bash curl gzip ripgrep busybox-extras nginx nginx-mod-http-js apache2-utils jq tzdata \
  && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && echo "Asia/Shanghai" > /etc/timezone && apk del tzdata \
  && rm -rf /tmp/* /var/cache/apk/* \
  && mv /usr/bin/rg /bin/grep

WORKDIR /opt/alist/
VOLUME /opt/alist/data/
COPY --from=builder /app/bin/alist ./

COPY entrypoint.sh /entrypoint.sh
RUN apk update && \
    apk upgrade --no-cache && \
    apk add --no-cache bash ca-certificates su-exec tzdata; \
    chmod +x /entrypoint.sh && \
    rm -rf /var/cache/apk/*
ENV PUID=0 PGID=0 UMASK=022
EXPOSE 5244 5245
CMD [ "/entrypoint.sh" ]
