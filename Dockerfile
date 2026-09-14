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
# The package was built in the stage above and never left the build, so no signature is needed.
# Afterwards the service user is re-created as 1000:1000 so bind mounts owned by the host's first user just work.
RUN apk add --no-cache --allow-untrusted /tmp/dist/jukem-*.apk \
 && rm -rf /tmp/dist \
 && deluser jukem && (delgroup jukem 2>/dev/null || true) \
 && addgroup -g 1000 jukem \
 && adduser -D -H -u 1000 -G jukem -s /sbin/nologin jukem \
 && addgroup jukem audio \
 && chown -R jukem:jukem /var/lib/jukem /var/log/jukem /srv/jukem
ENV JUKEM_RUNTIME=docker \
    JUKEM_LISTEN=":8080" \
    JUKEM_LOG_FILE=""
USER jukem
EXPOSE 8080
VOLUME /var/lib/jukem
HEALTHCHECK --interval=30s --timeout=5s CMD ["jukem", "healthcheck"]
ENTRYPOINT ["jukem", "serve", "--config", "/etc/jukem/config.yaml"]
