# Partage

Share files securely.

## Concepts

* client-side encryption with passphrase
* server knows nothing about the uploaded file
* short urls for download
* no dependencies
* made with go + html + vanilla js

## Deploy

### Configuration variables

The application is configured through environment variables:

| Name                | Description                                         | Default | Example           |
|---------------------|-----------------------------------------------------|---------|-------------------|
| `SITE_URL`          | the full URL of the site, as it appears to visitors | 1024    | 2048              |
| `MAX_FILE_SIZE_MB`  | maximum size of uploaded files in Mb                | 24      | 5000              |
| `CLEANUP_TIMER_MIN` | interval of time in minutes between pruning of old  | 10      | 60                |
| `SVG_LOGO`          | svg content for the logo                            | ""      | `<some svg data>` |


By default, the program listens on port `8080`.

## Building image

~~~
docker build --build-arg VERSION=1.0.0 -t deltablot/partage .
~~~
