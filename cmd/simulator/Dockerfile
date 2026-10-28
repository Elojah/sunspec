# Built from the repository's root, or from a Git URL, which builds from its root too:
#
#	docker build -f cmd/simulator/Dockerfile .
#	docker build -f cmd/simulator/Dockerfile https://github.com/Elojah/sunspec.git#v1.2.0
#
# The builder runs on the build platform and cross-compiles to the target's, such as --platform linux/arm64. No
# dependency, so no go.sum and nothing to download.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /sunspec
COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /simulator ./cmd/simulator

FROM scratch AS runner

COPY --from=builder --chmod=755 /simulator /simulator
# Docker lets a container's unprivileged user bind 502.
USER 65532:65532
EXPOSE 502
ENTRYPOINT ["/simulator"]
