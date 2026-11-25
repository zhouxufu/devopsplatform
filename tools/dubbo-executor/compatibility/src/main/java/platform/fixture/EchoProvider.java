package platform.fixture;
import org.apache.dubbo.config.*;
import java.nio.file.*;
import java.util.*;
public final class EchoProvider {
 public interface Echo { Long echo(Long value); String ping(); String fail(); }
 public static class EchoImpl implements Echo {
  public Long echo(Long v){return v;} public String ping(){return "pong";}
  public String fail(){throw new IllegalArgumentException("fixture exception");}
 }
 public static void main(String[] args)throws Exception{
  ApplicationConfig app=new ApplicationConfig("platform-dubbo-compatibility-"+args[2]+"-"+java.util.UUID.randomUUID().toString().substring(0,8));app.setQosEnable(false);
  app.setParameters(Collections.singletonMap("register-mode",args[2].startsWith("3")?"instance":"interface"));
  ServiceConfig<Echo> s=new ServiceConfig<>();s.setApplication(app);RegistryConfig registry=new RegistryConfig(args[0]);if(args[2].startsWith("3"))RegistryConfig.class.getMethod("setRegisterMode",String.class).invoke(registry,"instance");s.setRegistry(registry);if(args[2].startsWith("3"))s.setMetadataReportConfig(new MetadataReportConfig(args[0]));
  ProtocolConfig p=new ProtocolConfig(System.getProperty("fixture.protocol","dubbo"));try(java.net.ServerSocket socket=new java.net.ServerSocket(0)){p.setPort(socket.getLocalPort());}if(args[2].startsWith("3")){try(java.net.ServerSocket socket=new java.net.ServerSocket(0)){ApplicationConfig.class.getMethod("setMetadataServicePort",Integer.class).invoke(app,socket.getLocalPort());}}s.setProtocol(p);s.setInterface(Echo.class);s.setRef(new EchoImpl());s.setGroup("fixture");s.setVersion(args[2]);s.export();
  Files.write(Paths.get(args[1]),(s.getExportedUrls().get(0).getProtocol()+"://"+s.getExportedUrls().get(0).getAddress()).getBytes("UTF-8"));
  while(System.in.read()!=-1){}s.unexport();System.exit(0);
 }
}
