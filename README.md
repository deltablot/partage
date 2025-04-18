# Partage

Share files securely.

Try it: https://partage.deltablot.com

## Concepts

* client-side encryption with passphrase
* server knows nothing about the uploaded file
* extremely lightweight and fast
* made with go + html + vanilla js

The idea behind **Partage** is to have something that gets straight to the point, with no extra features, which means some choices have to be made.

No extra fonts, no runtime libraries, no i18n, simple and straightforward interface and codebase.

It is a very simple service to deploy, with a single container of about 10 Mb. There are no database, nothing fancy to setup. You launch the container and it runs.

We believe in lean software, meaning that we only add what is necessary. In the case of **Partage**, there are no runtime javascript dependencies, and a single go dependency (for UUID). The whole source code can be audited easily, because it's so small.

The css and javascript assets are minified and served with brotli compression. The whole application is only a few kb. That's a *small fraction* of the size of files loaded by other similar solutions.

If you're interested in having a file sharing service that is fast, easy to deploy, secure and pleasant to use, then use **Partage**.

## Deploy Partage

### Configuration variables

The application is configured through environment variables:

| Name                | Description                                         | Default             | Example                       |
|---------------------|-----------------------------------------------------|---------------------|-------------------------------|
| `SITE_URL`          | the full URL of the site, as it appears to visitors | http://localhost    | https://partage.deltablot.com |
| `MAX_FILE_SIZE_MB`  | maximum size of uploaded files in Mb                | 1024                | 2048                          |
| `MAX_TOTAL_FILES`   | maximum number of uploaded files                    | 24                  | 1337                          |
| `CLEANUP_TIMER_MIN` | interval of time in minutes between pruning of old  | 10                  | 60                            |
| `SVG_LOGO`          | svg content for the logo                            | ""                  | `<some svg data>`             |


By default, the program listens on port `8080`.

## Building image

~~~
docker build --build-arg VERSION=1.0.0 -t deltablot/partage .
~~~
