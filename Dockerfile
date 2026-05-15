FROM golang:1.26 AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0

RUN go build -o async-tasks .


FROM gcr.io/distroless/static-debian13:nonroot

COPY --from=builder /build/async-tasks /bin/async-tasks

ENTRYPOINT ["async-tasks"]
CMD ["--help"]
EXPOSE 60000

ARG git_commit=unknown
ARG version="1.0.0"
ARG descriptive_version=unknown

LABEL org.cyverse.git-ref="$git_commit"
LABEL org.cyverse.version="$version"
LABEL org.cyverse.descriptive-version="$descriptive_version"
LABEL org.label-schema.vcs-ref="$git_commit"
LABEL org.label-schema.vcs-url="https://github.com/cyverse-de/async-tasks"
LABEL org.label-schema.version="$descriptive_version"
