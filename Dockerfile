# syntax=docker/dockerfile:1

# Dockerfile for partage
# https://github.com/deltablot/partage

# STEP 1
# Node image to minify js and css files + brotli compression
# https://hub.docker.com/hardened-images/catalog/dhi/node/images/node%2Falpine-3.24%2F26-dev/sha256-8f0f7718a70430aede7dc2cbd0f9ef6a785fe5013cf66ca5677a2e9bae2bb4e6
FROM dhi.io/node@sha256:4b22d4f59496bc6873e5c48a474b9a4a82a6016c51d54afed3ab9d552f86aa0c AS bundler
RUN corepack enable \
    && corepack prepare yarn@stable --activate
RUN apk add --no-cache brotli bash
WORKDIR /home/node
USER node
COPY --chown=node:node src src
COPY --chown=node:node package.json src/
COPY yarn.lock src/
WORKDIR /home/node/src
RUN yarn install
RUN bash build.sh

# STEP 2
# Go builder
# https://hub.docker.com/hardened-images/catalog/dhi/golang/images/golang%2Falpine-3.24%2F1.27-dev/sha256-16c6c30bba8ee464931c026ca85c08ff4d8cb900d57a24b2d3676bf5c20e63b7
FROM dhi.io/golang@sha256:2852e3a139abb33e0b609030416a89d69e3f713ab21ed13470fe3efdca791a8c AS gobuilder
# this is set at build time
ARG VERSION=docker
# get logo
RUN apk add --no-cache curl
ARG SVG_LOGO_URL="https://www.deltablot.com/img/deltablot-purple.svg"
WORKDIR /app
# install dependencies
COPY go.mod .
COPY go.sum .
RUN go mod download
# copy code
COPY src src
COPY --from=bundler /home/node/src/dist src/dist
# disable CGO or it doesn't work in scratch
# target linux/amd64
# -w turn off DWARF debugging
# -s turn off symbol table
# change version at linking time
RUN export SVG_LOGO=$(curl -fsL ${SVG_LOGO_URL}) \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s -X 'main.svgLogo=${SVG_LOGO}' -X 'main.partageVersion=${VERSION}'" -o /partage ./src/main.go

# use busybox to create a correctly chown dir
# https://hub.docker.com/hardened-images/catalog/dhi/busybox/images/busybox%2Falpine-3.24%2F1-dev/sha256-2a35e95f106bcf78cc128618abdea150cb93c172b6cc8bdeee359b47d818cdef
FROM dhi.io/busybox@sha256:e625a8b84be579bb84c27fcbc6e01d6dbe4b934662e8cc9881664743f692353a AS prepare
RUN mkdir -p /var/partage \
    && chown nobody:nobody /var/partage

# use distroless instead of scratch to have ssl certificates and nobody
# dev: use :debug tag to have shell
#FROM gcr.io/distroless/static:debug
# https://hub.docker.com/hardened-images/catalog/dhi/static/images/static%2Falpine-3.24%2Fstatic/sha256-1e0311fd1463e15ab562ca81b287bde73e58327126678da98cf67b8efc8d3a81
FROM dhi.io/static@sha256:93568eb7c673afb3ad79b15cca341469d3e02cf859caae1049aa22fe7fbce90a
# copy the pre‑owned directory
COPY --from=prepare --chown=nobody:nobody /var/partage /var/partage
COPY --from=gobuilder /partage /usr/local/bin/partage
USER nobody:nobody
EXPOSE 8080
WORKDIR /srv/partage
ENTRYPOINT ["/usr/local/bin/partage"]
