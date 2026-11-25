package platform.dubbo;

import com.fasterxml.jackson.databind.*;
import java.io.*;
import java.util.*;
import org.apache.dubbo.config.*;
import org.apache.dubbo.rpc.*;
import org.apache.dubbo.rpc.cluster.ClusterInvoker;
import org.apache.dubbo.rpc.service.GenericService;
import org.apache.dubbo.rpc.service.GenericException;
import org.apache.dubbo.common.URL;

/** One bounded request per isolated process. No shell, uploaded classes, scripts or automatic retries. */
public final class Executor {
 static final ObjectMapper JSON = new ObjectMapper().enable(DeserializationFeature.USE_BIG_DECIMAL_FOR_FLOATS).enable(DeserializationFeature.USE_BIG_INTEGER_FOR_INTS);
 public static void main(String[] args) throws Exception {
  PrintStream output=System.out;System.setOut(System.err);
  Map<String,Object> result;
  try {
   ByteArrayOutputStream b=new ByteArrayOutputStream();byte[] buf=new byte[8192];int n;
   while((n=System.in.read(buf))!=-1){if(b.size()+n>512*1024)throw new IllegalArgumentException();b.write(buf,0,n);}
   result=execute(JSON.readTree(b.toByteArray()));
  } catch(Throwable e){result=new LinkedHashMap<>();result.put("error","Dubbo 执行器失败："+e.getClass().getSimpleName());}
  output.println(JSON.writeValueAsString(result));output.flush();System.exit(0);
 }
 static Map<String,Object> execute(JsonNode in) throws Exception {
  boolean discover="discover".equals(in.path("action").asText());
  if(!discover&&!"invoke".equals(in.path("action").asText()))throw new IllegalArgumentException();
  JsonNode rpc=in.path("rpc");String iface=rpc.path("interface").asText();
  ReferenceConfig<GenericService> ref=new ReferenceConfig<>();
  ApplicationConfig app=new ApplicationConfig("platform-dubbo-testing");app.setQosEnable(false);
  ref.setApplication(app);ref.setInterface(iface);ref.setGeneric("true");ref.setInjvm(false);ref.setCheck(false);ref.setRetries(0);ref.setCluster("failfast");ref.setTimeout(Math.max(1000,Math.min(120000,in.path("timeout_ms").asInt(10000))));
  ref.setGroup(rpc.path("group").asText());ref.setVersion(rpc.path("version").asText());
  if(discover){
   JsonNode registry=in.path("registry");RegistryConfig r=new RegistryConfig(registry.path("address").asText());r.setRegister(false);
   Map<String,String> params=new HashMap<>();params.put("namespace",registry.path("namespace").asText());params.put("group",registry.path("group").asText("DEFAULT_GROUP"));
   params.put("username",registry.path("username").asText());params.put("password",registry.path("password").asText());r.setParameters(params);ref.setRegistry(r);MetadataReportConfig metadata=new MetadataReportConfig(r.getAddress());metadata.setParameters(params);metadata.setUsername(registry.path("username").asText());metadata.setPassword(registry.path("password").asText());ref.setMetadataReportConfig(metadata);
  } else {
   URL endpoint=URL.valueOf(in.path("endpoint").asText());
   if(!Arrays.asList("dubbo","tri").contains(endpoint.getProtocol())||endpoint.getPort()<1||endpoint.getUsername()!=null||!endpoint.getParameters().isEmpty())throw new IllegalArgumentException();
   ref.setUrl(endpoint.setPath(iface).toFullString());
  }
  Map<String,Object> out=new LinkedHashMap<>();
  try {
   GenericService service=ref.get();
   if(discover){
    TreeSet<String> endpoints=new TreeSet<>();long until=System.currentTimeMillis()+Math.min(10000,in.path("timeout_ms").asInt(10000));
    do {collect(ref.getInvoker(),endpoints,0);if(!endpoints.isEmpty())break;Thread.sleep(100);}while(System.currentTimeMillis()<until);
    out.put("providers",endpoints);return out;
   }
   JsonNode types=rpc.path("parameter_types"),args=in.path("arguments");
   if(!types.isArray()||!args.isArray()||types.size()!=args.size()||types.size()>32)throw new IllegalArgumentException();
   String[] names=new String[types.size()];Object[] values=new Object[types.size()];
   for(int i=0;i<names.length;i++){names[i]=types.get(i).asText();values[i]=JSON.convertValue(args.get(i),Object.class);}
   Map<String,String> attachments=new HashMap<>();in.path("attachments").fields().forEachRemaining(e->attachments.put(e.getKey(),e.getValue().asText()));
   RpcContext.getClientAttachment().setAttachments(attachments);
   try {Object value=service.$invoke(rpc.path("method").asText(),names,values);out.put("body",value);out.put("success",true);}
   catch(GenericException e){out.put("exception_type",e.getExceptionClass());out.put("exception_message",e.getExceptionMessage());out.put("success",false);}
   catch(RpcException e){out.put("error","Dubbo 调用失败："+e.getCode());out.put("outcome_unknown",true);}
   finally{RpcContext.removeClientAttachment();}
   return out;
  } finally {ref.destroy();}
 }
 static void collect(Invoker<?> invoker,Set<String> endpoints,int depth){
  if(invoker==null||depth>8||endpoints.size()>100)return;
  if(invoker instanceof ClusterInvoker){
   org.apache.dubbo.rpc.cluster.Directory<?> dir=((ClusterInvoker<?>)invoker).getDirectory();
   if(dir!=null)for(Invoker<?> i:dir.getAllInvokers())collect(i,endpoints,depth+1);
  }else{
   URL u=invoker.getUrl();if(invoker.isAvailable()&&Arrays.asList("dubbo","tri").contains(u.getProtocol()))endpoints.add(u.getProtocol()+"://"+u.getAddress());
  }
 }
}
