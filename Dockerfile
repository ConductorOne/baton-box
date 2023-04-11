FROM gcr.io/distroless/static-debian11:nonroot
ENTRYPOINT ["/baton-box"]
COPY baton-box /