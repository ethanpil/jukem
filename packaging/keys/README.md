# Signing key

`jukem.rsa.pub` is the public half of the release signing key. Install it as
`/etc/apk/keys/jukem.rsa.pub` so that `apk add` accepts release packages
without `--allow-untrusted`.

Generate the pair once, on a trusted machine:

```sh
openssl genrsa -traditional -out jukem.rsa 4096
openssl rsa -in jukem.rsa -pubout -out jukem.rsa.pub
```

Commit `jukem.rsa.pub` here. Store the private key as the GitHub Actions
secret `APK_SIGNING_KEY` and keep an offline backup. Do not commit the private
key.
