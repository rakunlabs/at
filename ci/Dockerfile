ARG ALPINE=alpine:3.23.3

FROM $ALPINE

RUN apk --no-cache --no-progress add tzdata ca-certificates bash git curl ripgrep sed openssh-client-default

COPY at /
ENTRYPOINT [ "/at" ]
