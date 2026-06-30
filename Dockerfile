ARG GO_VERSION=1.25.4
ARG ALPINE_VERSION=3.22

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder
ARG VERSION=develop
ARG RELEASE_DATE=
RUN apk add --no-cache --no-progress \
    gcc git musl-dev
WORKDIR /src
ENV CGO_ENABLED=1 \
    CGO_CFLAGS="-D_LARGEFILE64_SOURCE"

COPY go.mod go.sum ./
RUN go mod download all

COPY cmd/dashboard ./cmd/dashboard
COPY model ./model
COPY pkg ./pkg
COPY proto ./proto
COPY service ./service
RUN go build -trimpath -ldflags="-s -w -X github.com/r0n9/nodekeep/service/dao.Version=${VERSION} -X github.com/r0n9/nodekeep/service/dao.ReleaseDate=${RELEASE_DATE}" -o /out/nodekeep-dashboard ./cmd/dashboard

FROM alpine:${ALPINE_VERSION}
ENV TZ="Asia/Shanghai"
RUN apk add --no-cache --no-progress \
    ca-certificates \
    tzdata && \
    cp "/usr/share/zoneinfo/$TZ" /etc/localtime && \
    echo "$TZ" >  /etc/timezone
WORKDIR /dashboard
COPY --from=builder /out/nodekeep-dashboard ./app
COPY resource ./resource
COPY script/config.yaml ./defaults/config.yaml
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

VOLUME ["/dashboard/data"]
EXPOSE 8008
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/dashboard/app"]
