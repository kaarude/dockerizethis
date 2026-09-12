# Java detection

`java.Detector{}` reads Maven XML, Gradle build text, and Java/Kotlin sources.
It does not run build tools, resolve dependencies, or interpolate Maven properties.
Markers include pom.xml, Gradle build/settings files, and mvnw/gradlew. When both
build tools exist, a sole wrapper selects its tool; otherwise Maven wins.

The plan records a numeric Java version, build command, main class, framework,
backing services, and literal System.getenv names. Maven test dependencies and
managed dependencies do not select services. JPA alone does not select a database.
Java version `1.8` normalizes to image version `8`.

Web-specific dependencies or JDK HTTP server source identify web processes.
Ports come from PORT fallbacks, socket literals, Ktor embeddedServer declarations,
or server.port configuration, with 8080 as the fallback. Build outputs and test
sources are skipped. Direct JDK createContext declarations preserve exact health
paths. Annotation routes, nested routers, strings, comments, and handlers that
inspect the request method do not establish a GET health probe.

Quarkus starts `/app/quarkus-app/quarkus-run.jar` with its complete distribution.
Spring Boot and Micronaut use `java -jar /app/app.jar`. Other detected main classes
use the jar classpath. A note warns when external dependencies may require a
shade, assembly, or shadow plugin to produce a self-contained jar.
