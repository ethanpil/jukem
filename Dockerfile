# syntax=docker/dockerfile:1

# Build stage: runs natively on the build machine and cross-compiles for the target.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETARCH
ARG VERSION=0.0.0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN scripts/package.sh "$TARGETARCH" "$VERSION"

# Runtime stage: the same apk that bare-metal installs use.
FROM alpine:3.24
COPY --from=build /src/dist/ /tmp/dist/
# The build stage above made the package and it did not leave the build, so it needs no signature.
# Then the image makes the service user again as 1000:1000, the usual owner of a host bind mount.
RUN apk add --no-cache --allow-untrusted /tmp/dist/jukem-*.apk \
 && rm -rf /tmp/dist \
 && deluser jukem && (delgroup jukem 2>/dev/null || true) \
 && addgroup -g 1000 jukem \
 && adduser -D -H -u 1000 -G jukem -s /sbin/nologin jukem \
 && addgroup jukem audio \
 && chown -R jukem:jukem /var/lib/jukem /var/log/jukem /srv/jukem
ENV JUKEM_RUNTIME=docker \
    JUKEM_LISTEN=":8080" \
    JUKEM_LISTEN_TLS=":8443" \
    JUKEM_LOG_FILE=""
USER jukem
EXPOSE 8080 8443
VOLUME /var/lib/jukem
HEALTHCHECK --interval=30s --timeout=5s CMD ["jukem", "healthcheck"]
ENTRYPOINT ["jukem", "serve", "--config", "/etc/jukem/config.yaml"]
