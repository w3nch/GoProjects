# GoServer — Simple form receiver

This is a small Go web server that serves a static site and accepts form submissions.

![Basic server flow graph](Photos/Basic%20server%20flow%20graph.jpg)

- Serves static files from the project `static/` directory.
- GET `/form` returns the HTML form page.
- POST `/form` accepts `name`, `email`, and `payload` fields and appends them to `submissions.txt`.

Preview of the Home page:
![Home Page](Photos/Home%20page.jpg)

Preview of the form page:

![Form submission page](Photos/Form%20submission%20page.jpg)

Getting started
--------------

1. Start the server (listen on port `8080`):

```bash
cd "/mnt/D/Code/Projects/Go/Building a web server"
go run main.go
```

2. Open the form in your browser:

```
http://localhost:8080/form
```

Submit from the command line
----------------------------

Use this `curl` example to POST a submission:

```bash
curl -X POST \
  -d "name=Alice&email=alice@example.com&payload={\"status\":\"ok\"}" \
  http://localhost:8080/form
```

Notes
-----
- Submitted entries are appended to `submissions.txt` in the project root.
- Images used by the project are in the `Photos/` folder.

If you want, I can start the server here and run a quick test POST to verify `submissions.txt` is updated.
