FROM golang AS build
ENV GO111MODULE=on
RUN mkdir -p /go/src/github.com/exoscale-labs/snap-o-matic
WORKDIR /go/src/github.com/exoscale-labs/snap-o-matic/
COPY . /go/src/github.com/exoscale-labs/snap-o-matic
RUN make build
RUN chmod +x /go/src/github.com/exoscale-labs/snap-o-matic/build/snap-o-matic

FROM alpine AS run
COPY --from=build /go/src/github.com/exoscale-labs/snap-o-matic/build/snap-o-matic /snap-o-matic
ENTRYPOINT ["/snap-o-matic"]
CMD ["-h"]
