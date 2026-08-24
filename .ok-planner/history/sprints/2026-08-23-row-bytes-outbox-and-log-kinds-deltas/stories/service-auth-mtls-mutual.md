---
story: service-auth-mtls-mutual
---

# Operator enables mutual TLS on internal service traffic

## Story

As an operator hardening a production deployment, I set `service_auth: mtls` and provide the CA encryption key, and every internal service↔service connection becomes mutually authenticated by CA-signed certificates — while my local dev and testcontainer stacks keep working unchanged with the default `none`, so I get an authenticated internal plane by one config flip and pay nothing for it when I don't need it.
