# Maintenance Warden: Deployment Guide

This guide provides detailed instructions for deploying the Maintenance Warden plugin in various environments, along with best practices and common deployment scenarios.

## Installation Methods

### Method 1: Traefik Static Configuration

1. Add the plugin to your Traefik static configuration:

```yaml
# traefik.yml or traefik.toml
experimental:
  plugins:
    maintenance-warden:
      moduleName: "github.com/TechAlchemistry/traefik-maintenance-warden"
      version: "v1.0.0"
```

2. Restart Traefik to load the plugin.

3. Configure the plugin in your dynamic configuration (see configuration examples below).

### Method 2: Kubernetes with Helm

If using the Traefik Helm chart:

1. Add the plugin to your Traefik Helm values:

```yaml
# values.yaml
experimental:
  plugins:
    maintenance-warden:
      moduleName: "github.com/TechAlchemistry/traefik-maintenance-warden"
      version: "v1.0.0"
```

2. Install or upgrade your Traefik chart:

```bash
helm upgrade --install traefik traefik/traefik -f values.yaml
```

3. Create a Middleware resource:

```yaml
apiVersion: traefik.containo.us/v1alpha1
kind: Middleware
metadata:
  name: maintenance-warden
  namespace: default
spec:
  plugin:
    maintenance-warden:
      # Choose one of these options:
      
      # For static file - mount a ConfigMap as a volume
      maintenanceFilePath: "/config/maintenance.html"
      
      # Or for service-based
      # maintenanceService: "http://maintenance-page-service.test-maintenance"
      
      # Or for content-based (simplest option)
      # maintenanceContent: "<html><body><h1>Maintenance in Progress</h1><p>Please check back later.</p></body></html>"
      
      # Other settings
      bypassHeader: "X-Maintenance-Bypass"
      bypassHeaderValue: "true"
      enabled: true
      statusCode: 503
      bypassPaths:
        - "/health"
        - "/api/status"
      logLevel: 1
      contentType: "text/html; charset=utf-8"
```

4. Reference the middleware in your IngressRoute:

```yaml
apiVersion: traefik.containo.us/v1alpha1
kind: IngressRoute
metadata:
  name: myapp
  namespace: default
spec:
  entryPoints:
    - web
  routes:
    - match: Host(`app.example.com`)
      kind: Rule
      middlewares:
        - name: maintenance-warden
      services:
        - name: myapp
          port: 80
```

## Configuration Examples

### Basic File-Based Maintenance (Recommended)

```yaml
# Dynamic configuration
http:
  middlewares:
    maintenance:
      plugin:
        maintenance-warden:
          maintenanceFilePath: "/path/to/maintenance.html"
          contentType: "text/html; charset=utf-8"
          bypassHeader: "X-Maintenance-Bypass"
          bypassHeaderValue: "true"
          enabled: true
          statusCode: 503
```

### Content-Based Maintenance

```yaml
# Dynamic configuration
http:
  middlewares:
    maintenance:
      plugin:
        maintenance-warden:
          maintenanceContent: "<html><body><h1>We're down for maintenance</h1><p>We'll be back shortly.</p></body></html>"
          contentType: "text/html; charset=utf-8"
          bypassHeader: "X-Maintenance-Bypass"
          bypassHeaderValue: "true"
          enabled: true
          statusCode: 503
```

### Service-Based Maintenance

```yaml
# Dynamic configuration
http:
  middlewares:
    maintenance:
      plugin:
        maintenance-warden:
          maintenanceService: "http://maintenance.internal:8080"
          bypassHeader: "X-Maintenance-Bypass"
          bypassHeaderValue: "true"
          enabled: true
          statusCode: 503
          maintenanceTimeout: 5
```

### Production-Grade Secure Configuration

```yaml
# Dynamic configuration
http:
  middlewares:
    secure-maintenance:
      plugin:
        maintenance-warden:
          maintenanceFilePath: "/etc/traefik/maintenance.html"
          contentType: "text/html; charset=utf-8"
          bypassHeader: "X-Service-Control-Token"  # Non-obvious name
          bypassHeaderValue: "a1b2c3d4e5f6g7h8i9j0"  # Random complex value
          enabled: true
          statusCode: 503
          bypassPaths:
            - "/health"
            - "/metrics"
          logLevel: 1
```

## Deployment Scenarios

### Scenario 1: Global Maintenance Mode

Apply maintenance mode to all services by attaching the middleware to the global entrypoint:

```yaml
# Dynamic configuration
http:
  routers:
    globalRouter:
      rule: "PathPrefix(`/`)"
      entryPoints:
        - web
        - websecure
      middlewares:
        - maintenance
      service: noop@internal
```

### Scenario 2: Service-Specific Maintenance

Apply maintenance mode to specific services only:

```yaml
# Dynamic configuration
http:
  routers:
    service-a:
      rule: "Host(`service-a.example.com`)"
      middlewares:
        - maintenance
      service: service-a
      
    service-b:
      rule: "Host(`service-b.example.com`)"
      # No maintenance middleware, remains available
      service: service-b
```

### Scenario 3: Annotation-Based Maintenance in Kubernetes

Use annotations to control maintenance mode for specific Ingress resources:

1. Configure the middleware to use annotation-based control:

```yaml
apiVersion: traefik.containo.us/v1alpha1
kind: Middleware
metadata:
  name: maintenance-warden
  namespace: default
spec:
  plugin:
    maintenance-warden:
      maintenanceContent: "<html><body><h1>Under Maintenance</h1><p>We'll be back shortly.</p></body></html>"
      bypassHeader: "X-Maintenance-Bypass"
      bypassHeaderValue: "secret-token"
      enabled: false  # Default to disabled
      statusCode: 503
      enabledAnnotation: "maintenance.example.com/enabled"
      enabledAnnotationValue: "true"
      enabledAnnotationHeader: "X-Kubernetes-Annotations"
```

2. Configure Traefik to forward Kubernetes annotations as headers:

```yaml
# Static Traefik configuration in values.yaml or traefik.yml
providers:
  kubernetesIngress:
    allowEmptyServices: true
    allowCrossNamespace: true
    # Forward specific annotations as headers
    annotationSelector: "maintenance.example.com/enabled"
```

3. Apply the middleware globally to all services:

```yaml
apiVersion: traefik.containo.us/v1alpha1
kind: IngressRoute
metadata:
  name: global-middleware
spec:
  entryPoints:
    - web
  routes:
    - match: PathPrefix(`/`)
      kind: Rule
      middlewares:
        - name: maintenance-warden
      priority: 1
      services:
        - name: noop@internal
          kind: TraefikService
```

4. Enable maintenance for specific services using annotations:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-service
  annotations:
    # This annotation will enable maintenance mode for this service
    maintenance.example.com/enabled: "true"
spec:
  rules:
    - host: service.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-service
                port:
                  number: 80
```

With this setup, you can enable or disable maintenance mode for any service by simply adding or removing the annotation, without changing the middleware configuration.

## Best Practices

### Maintenance File Management

1. **Version Control**: Keep maintenance HTML files in version control
2. **Template Variables**: Use a template system for dynamic content
3. **Responsive Design**: Ensure maintenance pages work on all devices
4. **Minimal Dependencies**: Avoid external resources on maintenance pages
5. **File Location**: Place maintenance files in a path accessible to Traefik

Example maintenance.html:

```html
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>System Maintenance</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Oxygen, Ubuntu, Cantarell, "Open Sans", "Helvetica Neue", sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 650px;
            margin: 0 auto;
            padding: 20px;
        }
        h1 {
            color: #2c3e50;
        }
        .maintenance-box {
            background-color: #f8f9fa;
            border-left: 4px solid #3498db;
            padding: 20px;
            border-radius: 4px;
            margin: 30px 0;
        }
        .estimated-time {
            font-weight: bold;
        }
    </style>
</head>
<body>
    <h1>System Maintenance</h1>
    <div class="maintenance-box">
        <p>We're currently performing scheduled maintenance on our systems.</p>
        <p>We expect to be back online by <span class="estimated-time">10:00 AM UTC on January 15, 2023</span>.</p>
        <p>Thank you for your patience.</p>
    </div>
    <p>If you have any questions, please contact support@example.com</p>
</body>
</html>
```

### Bypass Header Security

1. **Non-obvious Names**: Use non-obvious header names (not "maintenance-bypass")
2. **Complex Values**: Use complex, random string values (not "true" or "1")
3. **Rotation**: Periodically rotate header values
4. **Access Control**: Limit knowledge of bypass headers to authorized personnel
5. **Documentation**: Document the bypass mechanism for operations teams

### Kubernetes ConfigMap Integration

For Kubernetes deployments, store your maintenance HTML in a ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: maintenance-page
  namespace: default
data:
  maintenance.html: |
    <!DOCTYPE html>
    <html>
    <head>
        <title>System Maintenance</title>
        <!-- ... HTML content ... -->
    </head>
    <body>
        <h1>System Maintenance</h1>
        <!-- ... HTML content ... -->
    </body>
    </html>
```

Then mount it to Traefik and reference in your Middleware:

```yaml
apiVersion: traefik.containo.us/v1alpha1
kind: Middleware
metadata:
  name: maintenance-warden
spec:
  plugin:
    maintenance-warden:
      maintenanceFilePath: "/config/maintenance.html"
      # ... other settings ...
```

## Operational Procedures

### Enabling Maintenance Mode

#### For File-Based Configuration:

1. Update your dynamic configuration file:
   ```yaml
   http:
     middlewares:
       maintenance:
         plugin:
           maintenance-warden:
             enabled: true
   ```

2. Traefik will automatically reload the configuration.

#### For Kubernetes:

```bash
# Edit the middleware
kubectl edit middleware maintenance-warden

# Change the enabled: false to enabled: true
```

Or with a patch:

```bash
kubectl patch middleware maintenance-warden --type=json -p='[{"op": "replace", "path": "/spec/plugin/maintenance-warden/enabled", "value": true}]'
```

### Testing Maintenance Mode

Test that maintenance mode is working correctly:

1. Make a regular request (should see maintenance page):
   ```bash
   curl -I https://your-service.example.com
   ```

2. Make a request with bypass header (should access the service):
   ```bash
   curl -I -H "X-Maintenance-Bypass: true" https://your-service.example.com
   ```

### Disabling Maintenance Mode

Follow the same procedures as enabling, but set `enabled: false`.
