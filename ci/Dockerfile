ARG ALPINE=alpine:3.23.3

FROM $ALPINE

RUN apk --no-cache --no-progress add tzdata ca-certificates

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/pika /
ENTRYPOINT [ "/pika" ]
