# Java detection

`java.Detector{}` implements `detect.Detector` and reports stack `java`.
It reads pom.xml with regular expressions and Gradle build files as text.
It does not execute the build, resolve the dependency graph, or evaluate
Maven property interpolation. target/, build/, .gradle/, out/, and .git are
skipped when scanning sources.

Markers are pom.xml, build.gradle(.kts), settings.gradle(.kts), and the
mvnw/gradlew wrappers. When both build tools are present the wrapper wins,
otherwise Maven; a note records the ambiguity. The plan includes the Java
version (maven.compiler.* or java.version properties, Gradle toolchain or
compatibility settings, default 21), the build command (wrapper-aware),
frameworks, backing services from dependency coordinates, and literal
`System.getenv` names.

Only web-specific coordinates (spring-boot-starter-web, quarkus-rest,
micronaut-http-server, ktor-server, javalin, spark, ...) or a
`com.sun.net.httpserver` server in source make the process web — a bare
framework coordinate also matches CLI apps. Ports come from a PORT env
fallback, InetSocketAddress/ServerSocket literals, or server.port in
application configuration; the default is 8080. A `/health` route string in
source becomes HealthPath.

The start command is `java -jar /app/app.jar` for frameworks that repackage
a self-contained jar (spring-boot, quarkus, micronaut), and
`java -cp /app/app.jar <main>` when a main class or Gradle `mainClass` is
found — in that case a note warns when the build has no shade/assembly/
shadow plugin and dependencies exist.
