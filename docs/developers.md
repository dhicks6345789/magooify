# Building

Clone the repository:

```
git clone https://github.com/dhicks6345789/magooify.git
```

And run build:

```
cd magooify
bash build.sh build-all
```

This will compile the executables for all platforms.

You can build executables and generate documentation, including Swaggo's interactive API documentation, and copy the lot directly to somewhere they can be served as a web site to act as a project homepage using "build.sh dist". You just need to specify the path you want the files to go to, e.g.:

```
bash build.sh dist ~/www/magooify
```

# Extending

This project was written in Go with an AI agent running in OpenCode, under a permissive Apache license. It is designed to be easy to customise and extend, and you are free to use and change it to suite your users.
