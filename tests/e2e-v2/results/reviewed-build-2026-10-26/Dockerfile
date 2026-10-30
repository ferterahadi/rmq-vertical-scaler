FROM gcr.io/distroless/static:nonroot
COPY rmq-vertical-scaler /rmq-vertical-scaler
USER nonroot:nonroot
ENTRYPOINT ["/rmq-vertical-scaler"]
CMD ["run"]
