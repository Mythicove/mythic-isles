FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /isles ./cmd/isles

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -g 10001 isles && adduser -D -u 10001 -G isles isles && mkdir /data && chown isles:isles /data
COPY --from=build /isles /usr/local/bin/isles
USER isles
ENV ISLES_LISTEN=0.0.0.0:8095 ISLES_DATA_DIR=/data
EXPOSE 8095
ENTRYPOINT ["isles"]
