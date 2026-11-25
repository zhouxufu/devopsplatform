package platform.dubbo;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.util.*;
import java.util.concurrent.atomic.AtomicInteger;
import org.apache.dubbo.config.*;
import org.junit.jupiter.api.*;
import static org.junit.jupiter.api.Assertions.*;
class ExecutorTest {
 public interface Echo { Long echo(Long value); Map<String,Object> object(Map<String,Object> value); String ping(); String fail(); }
 public static class EchoImpl implements Echo {
  static final AtomicInteger calls=new AtomicInteger();
  public Long echo(Long v){calls.incrementAndGet();return v;}
  public Map<String,Object> object(Map<String,Object> v){calls.incrementAndGet();return v;}
  public String ping(){calls.incrementAndGet();return "pong";}
  public String fail(){calls.incrementAndGet();throw new IllegalArgumentException("fixture exception");}
 }
 static ServiceConfig<Echo> provider;
 @BeforeAll static void start() throws Exception{
  ApplicationConfig app=new ApplicationConfig("platform-dubbo-testing");app.setQosEnable(false);
  provider=new ServiceConfig<>();provider.setApplication(app);provider.setRegistry(new RegistryConfig("N/A"));
  int port;try(java.net.ServerSocket socket=new java.net.ServerSocket(0)){port=socket.getLocalPort();}
  ProtocolConfig protocol=new ProtocolConfig("dubbo");protocol.setHost("127.0.0.1");protocol.setPort(port);provider.setProtocol(protocol);
  provider.setInterface(Echo.class);provider.setRef(new EchoImpl());provider.setGroup("fixture");provider.setVersion("1");provider.export();
 }
 @AfterAll static void stop(){if(provider!=null)provider.unexport();}
 static ObjectNode input(String method,String types,String args)throws Exception{
  ObjectNode in=(ObjectNode)Executor.JSON.readTree("{\"action\":\"invoke\",\"timeout_ms\":5000,\"rpc\":{\"interface\":\""+Echo.class.getName()+"\",\"group\":\"fixture\",\"version\":\"1\",\"method\":\""+method+"\",\"parameter_types\":"+types+"},\"arguments\":"+args+"}");
  org.apache.dubbo.common.URL u=provider.getExportedUrls().get(0);in.put("endpoint","dubbo://"+u.getAddress());return in;
 }
 @Test void exactLongAndObject()throws Exception{
  Map<String,Object> r=Executor.execute(input("echo","[\"java.lang.Long\"]","[9223372036854775806]"));assertEquals(true,r.get("success"));assertEquals("9223372036854775806",r.get("body").toString());
  r=Executor.execute(input("object","[\"java.util.Map\"]","[{\"nested\":[1,2],\"label\":\"中文\"}]"));assertEquals(true,r.get("success"));assertTrue(r.get("body").toString().contains("中文"));
 }
 @Test void noArgumentsAndExceptionOnce()throws Exception{
  assertEquals("pong",Executor.execute(input("ping","[]","[]")).get("body"));
  int before=EchoImpl.calls.get();Map<String,Object> r=Executor.execute(input("fail","[]","[]"));assertEquals(false,r.get("success"));assertEquals("java.lang.IllegalArgumentException",r.get("exception_type"));assertEquals(before+1,EchoImpl.calls.get());
 }
}
