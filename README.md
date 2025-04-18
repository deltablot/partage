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

+--------------------+------------------+-------------------------------+----------------------------------------------------------+
|       Name         |     Default      |           Example             |                       Description                        |
+--------------------+------------------+-------------------------------+----------------------------------------------------------+
| `SITE_URL`          | http://localhost | https://partage.deltablot.com | the full URL of the site, as it appears to visitors      |
| `MAX_FILE_SIZE_MB`  | 1024             | 2048                          | maximum size of uploaded files in Mb                     |
| `MAX_TOTAL_FILES`   | 24               | 5000                          | maximum number of files that can be stored on the server |
| `CLEANUP_TIMER_MIN` | 10               | 60                            | interval of time in minutes between pruning of old files |
| `SVG_LOGO`          | ""               | `<some svg data>`             | svg content for the logo                                 |
+--------------------+------------------+-------------------------------+----------------------------------------------------------+

By default, the program listens on port `8080`.

## Building image

~~~
docker build --build-arg VERSION=1.0.0 -t deltablot/partage .
~~~
