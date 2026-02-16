# Builder stage for clawkerd
FROM golang:1.25-alpine AS clawkerd-builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY clawkerd/ ./clawkerd/
COPY internal/clawkerd/ ./internal/clawkerd/
COPY internal/logger/ ./internal/logger/
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/clawkerd ./clawkerd

# Final stage
FROM alpine:3.21
RUN apk add --no-cache bash su-exec tini
ARG USERNAME=claude
RUN addgroup -g 1001 ${USERNAME} && \
    adduser -D -u 1001 -G ${USERNAME} -s /bin/bash ${USERNAME}
RUN mkdir -p /var/run/clawker && chown ${USERNAME}:${USERNAME} /var/run/clawker
COPY --from=clawkerd-builder /out/clawkerd /usr/local/bin/clawkerd
COPY test/controlplane/testdata/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/clawkerd /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/sbin/tini", "--", "entrypoint.sh"]
CMD ["sleep", "infinity"]
