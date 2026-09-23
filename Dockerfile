# --- web (admin GUI) ---
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- go ---
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -tags prod -trimpath -ldflags="-s -w" -o /out/nano-llm-proxy .

# --- runtime ---
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/nano-llm-proxy /nano-llm-proxy
# Container-friendly defaults: listen on all interfaces, allow plain-HTTP
# logins (set "cookieSecure": true when fronting this with TLS).
RUN printf '{"port":8787,"bind":"0.0.0.0","cookieSecure":false}\n' > /etc-config.json \
    && mkdir -p /etc/nano-llm-proxy /data \
    && mv /etc-config.json /etc/nano-llm-proxy/config.json
WORKDIR /data
VOLUME /data
EXPOSE 8787
ENTRYPOINT ["/nano-llm-proxy", "/etc/nano-llm-proxy/config.json"]
