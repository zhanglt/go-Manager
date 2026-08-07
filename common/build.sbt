assembly / test := {}

assembly / assemblyMergeStrategy := {
  case PathList(ps @ _*) if ps.last endsWith "io.netty.versions.properties" => MergeStrategy.first
  case x                                                                    =>
    val strategy = (assembly / assemblyMergeStrategy).value(x)
    if (strategy == MergeStrategy.deduplicate) MergeStrategy.first
    else strategy
}
