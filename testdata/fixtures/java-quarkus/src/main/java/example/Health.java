package example;

import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;

@Path("/health")
public class Health {
    @GET
    public String health() { return "ok"; }
}
