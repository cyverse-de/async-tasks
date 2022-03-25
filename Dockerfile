FROM golang:1.17

WORKDIR /go/src/github.com/cyverse-de/async-tasks
COPY . .
ENV CGO_ENABLED=0
RUN go install

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
