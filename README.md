# Partage

Share files securely.

Try it: https://partage.deltablot.com

## Concepts

* client-side encryption with passphrase
* server knows nothing about the uploaded file
* extremely lightweight: about 11kb on cold page load
* made with go + html + vanilla js

The idea behind **Partage** is to have something that gets straight to the point, with no extra features, which means no extra code. It is a very simple service to deploy, with a single container of about 10 Mb.

Other projects have many more features, and way, way more code. We believe in lean software, meaning that we only add what is necessary. In the case of **Partage**, there are no runtime javascript dependencies, and a single go dependency (for UUID). The whole source code can be audited easily, because it's so small.

The css and javascript assets are minified and served with brotli compression. The whole thing is a bit more than 11kb to transfer. That's a *fraction* of the size of files loaded by other similar solutions.

If you're interested in having a file sharing service that is fast, easy to deploy, secure and pleasant to use, then use **Partage**.

## Comparison with similar tools

### PrivateBin

[PrivateBin](https://github.com/PrivateBin/PrivateBin) is a fork of ZeroBin, which was initiated a long time ago, as a good old PHP project. The PrivateBin project refactored a few things to make it maintainable, but it's a tool that shows its age:

- 15,000+ lines of JS code with hardcoded libraries in the repository like it's 2003
- 6,000+ lines of spaghetti PHP code
- PHP projects are a pain to deploy, because you also need php-fpm and a webserver, and update often
- One would need to work out containerization, as no container documentation is provided
- It has features we don't need (like S3 storage, it's cool but do we need that complexity for ephemeral files? No.)

### Password Pusher

[PasswordPusher](https://github.com/pglombardo/PasswordPusher) is a very complete project, written in ruby. It has many features, which means many lines of code to audit. It's a great tool, and actively maintained, but overly complicated if what you want is simply sharing a file securely with someone else.

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
