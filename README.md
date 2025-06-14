# fsets
Functional set execution

The idea here is similar to fqueue and my statemachine.  What we want to do is execute functions sequentially. We want them to pass a state object while still having access to a class.  

I'm hoping this might give us statemachine like behavior for testing instead of cascading calls that have to be faked.



