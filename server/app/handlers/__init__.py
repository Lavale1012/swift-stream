"""Request logic, kept free of FastAPI so it can be called and tested without
HTTP. The modules in app.routes validate input, call these, and turn the
results and errors into responses."""
